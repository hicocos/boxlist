"""Authentication, safe image conversion, and durable catalog storage."""
from __future__ import annotations

import hashlib
import http.client
import io
import ipaddress
import json
import os
import re
import socket
import sqlite3
import stat
import struct
import threading
import time
import uuid
import warnings
import zlib
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime, timezone
from html.parser import HTMLParser
from pathlib import Path
from typing import Callable, Iterator
from urllib.parse import unquote_to_bytes, urlsplit

from PIL import Image, ImageFile, ImageOps

MAX_INPUT_BYTES = 5 * 1024 * 1024
MAX_DIMENSION = 4096
MAX_PIXELS = 16_000_000
MAX_ASSETS = 200
MAX_ORIGINAL_BYTES = 256 * 1024 * 1024
ASSET_RE = re.compile(r"upload-[0-9a-f]{32}\Z", re.ASCII)
REQUEST_RE = re.compile(r"[0-9a-f]{32}\Z", re.ASCII)
BUILTIN_IDS = frozenset({
    "pack-cab", "pack-doc", "pack-docx", "pack-mp3", "pack-pdf", "pack-ppt", "pack-pptx",
    "pack-psd", "pack-rar", "pack-txt", "pack-wav", "pack-wma", "pack-xlsx", "pack-zip",
    "pack-download", "pack-image", "pack-folder", "pack-document", "pack-video", "pack-music",
    "legacy-smile",
})
Image.MAX_IMAGE_PIXELS = MAX_PIXELS
ImageFile.LOAD_TRUNCATED_IMAGES = False
# Configure once, not with racing per-thread warning contexts.
warnings.filterwarnings("error", category=Image.DecompressionBombWarning)


class Fault(Exception):
    def __init__(self, status: int, message: str):
        super().__init__(message)
        self.status, self.message = status, message


@dataclass(frozen=True)
class Config:
    root: Path = Path("/var/lib/openlist-icons")
    backend: str = "http://127.0.0.1:5244"
    origin: str = "https://openlist.example"
    max_input_bytes: int = MAX_INPUT_BYTES
    max_assets: int = MAX_ASSETS
    max_original_bytes: int = MAX_ORIGINAL_BYTES
    auth_timeout: float = 5.0
    header_timeout: float = 5.0
    body_timeout: float = 20.0
    idle_timeout: float = 3.0
    database_timeout: float = 2.0
    max_connections: int = 16

    @classmethod
    def from_env(cls) -> "Config":
        # No environment variable can disable authentication or raise limits.
        return cls(
            root=Path(os.environ.get("OPENLIST_ICONS_ROOT", str(cls.root))),
            backend=os.environ.get("OPENLIST_ICONS_BACKEND", cls.backend),
            origin=os.environ.get("OPENLIST_ICONS_ORIGIN", cls.origin),
        )

    def validate(self) -> None:
        if not self.root.is_absolute():
            raise ValueError("data root must be absolute")
        parsed = urlsplit(self.backend)
        try:
            local = ipaddress.ip_address(parsed.hostname or "").is_loopback
            port = parsed.port
        except ValueError:
            local, port = False, None
        if (parsed.scheme != "http" or not local or port is None
                or parsed.username is not None or parsed.password is not None
                or parsed.path not in ("", "/") or parsed.query or parsed.fragment):
            raise ValueError("backend must be an explicit loopback HTTP origin")
        site = urlsplit(self.origin)
        if (site.scheme != "https" or not site.hostname or site.username is not None
                or site.password is not None or site.path or site.query or site.fragment):
            raise ValueError("origin must be an exact HTTPS origin without trailing slash")
        if not 0 < self.max_input_bytes <= MAX_INPUT_BYTES:
            raise ValueError("invalid input limit")
        if not 0 < self.max_assets <= MAX_ASSETS:
            raise ValueError("invalid asset limit")
        if not 0 < self.max_original_bytes <= MAX_ORIGINAL_BYTES:
            raise ValueError("invalid storage limit")
        if any(not 0 < value <= maximum for value, maximum in (
                (self.auth_timeout, 5.0), (self.header_timeout, 5.0),
                (self.body_timeout, 20.0), (self.idle_timeout, 3.0),
                (self.database_timeout, 2.0))):
            raise ValueError("invalid timeout")
        if not 1 <= self.max_connections <= 16:
            raise ValueError("invalid connection limit")


@dataclass(frozen=True)
class Admin:
    user_id: str


class OpenListVerifier:
    """Real GET /api/me, strict role/disabled/id, no redirects or token logging."""
    def __init__(self, config: Config):
        config.validate()
        parsed = urlsplit(config.backend)
        self.host, self.port = parsed.hostname, parsed.port
        self.timeout = config.auth_timeout

    def _get(self, authorization: str, path: str, *, max_bytes: int = 65536) -> dict:
        validate_authorization(authorization)
        deadline = time.monotonic() + self.timeout
        connection = http.client.HTTPConnection(self.host, self.port, timeout=self.timeout)
        watchdog = None
        raw_socket = None
        timed_out = threading.Event()
        try:
            connection.connect()
            raw_socket = connection.sock

            def interrupt() -> None:
                timed_out.set()
                try:
                    raw_socket.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass

            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise Fault(504, "权限校验超时")
            # shutdown interrupts headers/body even when they drip bytes below
            # the socket's idle timeout. A retained socket also covers responses
            # whose Connection: close detaches HTTPConnection.sock.
            watchdog = threading.Timer(remaining, interrupt)
            watchdog.daemon = True
            watchdog.start()
            connection.request("GET", path, headers={
                "Authorization": authorization,
                "Accept": "application/json",
                "Connection": "close",
            })
            response = connection.getresponse()
            if response.status in (401, 403):
                raise Fault(response.status, "登录无效或权限不足")
            if response.status != 200:
                raise Fault(502, "权限校验失败")
            if len(response.getheaders()) > 64:
                raise Fault(502, "权限校验失败")
            payload = response.read(max_bytes + 1)
            if timed_out.is_set() or time.monotonic() >= deadline:
                raise Fault(504, "权限校验超时")
            if len(payload) > max_bytes:
                raise Fault(502, "权限校验失败")
            data = json.loads(payload, object_pairs_hook=_unique_json)
            if not isinstance(data, dict) or type(data.get("code")) is not int:
                raise Fault(502, "权限校验失败")
            if data["code"] in (401, 403):
                raise Fault(data["code"], "登录无效或权限不足")
            if data["code"] != 200 or not isinstance(data.get("data"), dict):
                raise Fault(502, "权限校验失败")
            return data["data"]
        except Fault:
            raise
        except (socket.timeout, TimeoutError):
            raise Fault(504, "权限校验超时") from None
        except (OSError, http.client.HTTPException, ValueError, TypeError, RecursionError):
            if timed_out.is_set() or time.monotonic() >= deadline:
                raise Fault(504, "权限校验超时") from None
            raise Fault(502, "权限校验失败") from None
        finally:
            if watchdog is not None:
                watchdog.cancel()
                watchdog.join()
            connection.close()
            if raw_socket is not None:
                raw_socket.close()

    def __call__(self, authorization: str) -> Admin:
        user = self._get(authorization, "/api/me")
        if type(user.get("role")) is not int or user["role"] != 2 or user.get("disabled") is not False:
            raise Fault(403, "仅启用的管理员可管理素材")
        if type(user.get("id")) is not int or user["id"] <= 0:
            raise Fault(502, "权限校验失败")
        return Admin(str(user["id"]))

    def read_head(self, authorization: str) -> str:
        try:
            setting = self._get(authorization, "/api/admin/setting/get?key=customize_head", max_bytes=1024 * 1024)
            if (not isinstance(setting.get("value"), str)
                    or setting.get("key", "customize_head") != "customize_head"):
                raise Fault(502, "无法核对图标配置，未删除")
            return setting["value"]
        except Fault as error:
            if error.status in (401, 403):
                raise
            raise Fault(error.status, "无法核对图标配置，未删除") from None


class _ThemeMetaParser(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.contents = []
        self.invalid = False

    def handle_starttag(self, tag, attrs):
        if tag != "meta":
            return
        ids = [value for key, value in attrs if key == "id"]
        if "openlist-icon-config" not in ids:
            return
        contents = [value for key, value in attrs if key == "content"]
        if len(ids) != 1 or len(contents) != 1 or contents[0] is None:
            self.invalid = True
        else:
            self.contents.append(contents[0])

    handle_startendtag = handle_starttag


def _unique_json(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def head_references(head: str) -> set[str]:
    """Nonexecuting, fail-closed parsing, with legacy smile compatibility."""
    parser = _ThemeMetaParser()
    try:
        if not isinstance(head, str) or len(head.encode("utf-8", "strict")) > 1024 * 1024:
            raise ValueError("invalid head")
        parser.feed(head)
        parser.close()
        if parser.invalid or len(parser.contents) > 1:
            raise ValueError("ambiguous theme meta")
        refs = set()
        if parser.contents:
            content = parser.contents[0]
            if re.search(r"%(?![0-9a-fA-F]{2})", content):
                raise ValueError("bad percent encoding")
            decoded = unquote_to_bytes(content).decode("utf-8", "strict")
            raw = json.loads(decoded, object_pairs_hook=_unique_json)
            if (not isinstance(raw, dict) or type(raw.get("version")) is not int or raw["version"] != 1
                    or not isinstance(raw.get("selections"), dict) or len(raw["selections"]) > 512):
                raise ValueError("invalid theme")
            for key, value in raw["selections"].items():
                if not isinstance(key, str) or not isinstance(value, str):
                    raise ValueError("invalid selection")
                if value not in BUILTIN_IDS and not ASSET_RE.fullmatch(value) and value != "default":
                    raise ValueError("unknown selection")
                refs.add(value)
        elif "OPENLIST-ICON-THEME" in head or "openlist-icon-config" in head:
            raise ValueError("unparseable theme")
        if "OPENLIST-FOLDER-ICON" in head or "OPENLIST_FOLDER_ICON_STYLE" in head:
            # Never execute the historical script. Ambiguous legacy values
            # fail closed; smile is conservatively retained even beside meta.
            blocks = re.findall(
                r"<!-- OPENLIST-FOLDER-ICON-START -->[\s\S]*?<!-- OPENLIST-FOLDER-ICON-END -->", head)
            if not blocks:
                raise ValueError("unparseable legacy theme")
            for block in blocks:
                values = re.findall(r"OPENLIST_FOLDER_ICON_STYLE\s*=\s*([\"'])([^\"']*)\1", block)
                if not values:
                    raise ValueError("invalid legacy theme")
                for _, value in values:
                    if value == "smile":
                        refs.add("legacy-smile")
                    elif value not in ("default", "native"):
                        raise ValueError("unknown legacy theme")
        return refs
    except (ValueError, TypeError, UnicodeError, RecursionError):
        raise Fault(502, "无法核对图标配置，未删除") from None


def validate_authorization(value: str | None) -> str:
    if (not value or value != value.strip() or len(value) > 4096
            or any(ord(char) < 32 or ord(char) > 126 for char in value)):
        raise Fault(401, "请先登录管理员账号")
    return value


def decode_name(value: str | None) -> str:
    import unicodedata
    if (not value or len(value) > 3072 or any(ord(c) > 126 or ord(c) < 33 for c in value)
            or re.search(r"%(?![0-9a-fA-F]{2})", value)):
        raise Fault(400, "文件名无效")
    try:
        name = unquote_to_bytes(value).decode("utf-8", "strict")
    except UnicodeError:
        raise Fault(400, "文件名无效") from None
    if (not name.strip() or name in (".", "..") or len(name.encode("utf-16-le")) // 2 > 160
            or len(name.encode("utf-8")) > 1024 or "/" in name or "\\" in name
            or any(unicodedata.category(c).startswith("C") for c in name)):
        raise Fault(400, "文件名无效")
    if not name.lower().endswith(".png"):
        raise Fault(415, "仅支持 PNG 静态图片")
    return name


def check_dimensions(width: int, height: int) -> None:
    if width <= 0 or height <= 0:
        raise Fault(422, "图片无效或损坏")
    if width > MAX_DIMENSION or height > MAX_DIMENSION or width * height > MAX_PIXELS:
        raise Fault(413, "图片尺寸超出限制")


def _check_png_container(data: bytes) -> None:
    """Bound dimensions before Pillow; check PNG framing/CRC and reject APNG."""
    if (len(data) < 33 or data[8:16] != b"\x00\x00\x00\rIHDR"):
        raise Fault(422, "图片无效或损坏")
    check_dimensions(*struct.unpack_from(">II", data, 16))
    offset, ended, idat_seen = 8, False, False
    while offset + 12 <= len(data):
        size = struct.unpack_from(">I", data, offset)[0]
        chunk = data[offset + 4:offset + 8]
        if offset + size + 12 > len(data):
            raise Fault(422, "图片无效或损坏")
        if chunk == b"IHDR" and offset != 8:
            raise Fault(422, "图片无效或损坏")
        checksum = struct.unpack_from(">I", data, offset + 8 + size)[0]
        if zlib.crc32(data[offset + 4:offset + 8 + size]) & 0xffffffff != checksum:
            raise Fault(422, "图片无效或损坏")
        if chunk in (b"acTL", b"fcTL", b"fdAT"):
            raise Fault(415, "不支持动画 PNG")
        if chunk == b"IDAT":
            idat_seen = True
        offset += size + 12
        if chunk == b"IEND":
            ended = size == 0 and offset == len(data)
            break
    if not ended or not idat_seen:
        raise Fault(422, "图片无效或损坏")


def convert_image(data: bytes) -> bytes:
    if not data or len(data) > MAX_INPUT_BYTES:
        raise Fault(413, "文件大小超出限制")
    if not data.startswith(b"\x89PNG\r\n\x1a\n"):
        raise Fault(415, "仅支持 PNG 静态图片")
    try:
        _check_png_container(data)
        with Image.open(io.BytesIO(data)) as initial:
            if initial.format != "PNG":
                raise Fault(415, "仅支持 PNG 静态图片")
            check_dimensions(*initial.size)
            if getattr(initial, "is_animated", False) or getattr(initial, "n_frames", 1) > 1:
                raise Fault(415, "不支持动画图片")
            initial.verify()
        with Image.open(io.BytesIO(data)) as source:
            frame = source
            check_dimensions(*frame.size)
            frame.load()
            oriented = ImageOps.exif_transpose(frame)
            check_dimensions(*oriented.size)
            rgba = oriented.convert("RGBA")
            rgba.thumbnail((232, 232), Image.Resampling.LANCZOS)
            canvas = Image.new("RGBA", (256, 256), (0, 0, 0, 0))
            canvas.alpha_composite(rgba, ((256 - rgba.width) // 2, (256 - rgba.height) // 2))
            canvas.info.clear()
            output = io.BytesIO()
            canvas.save(output, format="WEBP", lossless=True, method=4, exact=True)
            return output.getvalue()
    except Fault:
        raise
    except (Image.DecompressionBombWarning, Image.DecompressionBombError):
        raise Fault(413, "图片尺寸超出限制") from None
    except (OSError, ValueError, SyntaxError, EOFError, struct.error, IndexError, KeyError):
        raise Fault(422, "图片无效或损坏") from None


def _private_directory(path: Path) -> None:
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    info = path.lstat()
    if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid()
            or info.st_mode & 0o022):
        raise ValueError("data directories must be owned by the service and not writable by other users")
    os.chmod(path, 0o700, follow_symlinks=False)


def _fsync_dir(path: Path) -> None:
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


class CatalogStore:
    """Two immutable files + WAL row; only committed rows are public."""
    def __init__(self, config: Config):
        config.validate()
        self.config = config
        self.root = config.root
        self.originals, self.previews, self.staging = (self.root / name for name in ("originals", "previews", ".staging"))
        self.trash = self.root / "trash"
        self.trash_originals, self.trash_previews = self.trash / "originals", self.trash / "previews"
        for path in (self.root, self.originals, self.previews, self.staging,
                     self.trash, self.trash_originals, self.trash_previews):
            _private_directory(path)
        self.database = self.root / "catalog.db"
        if self.database.exists() or self.database.is_symlink():
            info = self.database.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o022:
                raise ValueError("unsafe catalog database")
            os.chmod(self.database, 0o600, follow_symlinks=False)
        else:
            fd = os.open(self.database, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
            os.close(fd)
        with self._connect() as connection:
            connection.execute("PRAGMA journal_mode=WAL")
            connection.execute("""CREATE TABLE IF NOT EXISTS assets (
                id TEXT PRIMARY KEY,
                owner TEXT NOT NULL,
                request_id TEXT NOT NULL,
                content_sha TEXT NOT NULL,
                name TEXT NOT NULL,
                width INTEGER NOT NULL CHECK(width=256),
                height INTEGER NOT NULL CHECK(height=256),
                bytes INTEGER NOT NULL CHECK(bytes>0),
                original_bytes INTEGER NOT NULL CHECK(original_bytes>0),
                created_at TEXT NOT NULL,
                deleted_at TEXT,
                UNIQUE(owner, request_id)
            )""")
            if "deleted_at" not in {row[1] for row in connection.execute("PRAGMA table_info(assets)")}:
                connection.execute("ALTER TABLE assets ADD COLUMN deleted_at TEXT")
            connection.execute("CREATE TABLE IF NOT EXISTS hidden_builtins (id TEXT PRIMARY KEY, deleted_at TEXT NOT NULL)")
            connection.execute("BEGIN IMMEDIATE")
            # A process crash can strand files before the catalog commit. Only
            # unregistered files are removed; registered originals remain until
            # an authenticated explicit deletion retires them privately.
            registered = {row[0] for row in connection.execute("SELECT id FROM assets")}
            for directory, suffix in ((self.originals, ".original"), (self.previews, ".webp")):
                for path in directory.iterdir():
                    stem = path.name.removesuffix(suffix)
                    if path.name.endswith(suffix) and ASSET_RE.fullmatch(stem) and stem not in registered:
                        path.unlink()
            # Staging creation and installation also hold BEGIN IMMEDIATE, so
            # no other live writer can own a .tmp while this lock is held.
            # Clean crash remnants immediately rather than accumulating disk use.
            for path in self.staging.iterdir():
                if re.fullmatch(r"[0-9a-f]{32}\.tmp", path.name):
                    path.unlink()
            # Deleted rows are durable tombstones: a crash before/halfway through
            # retirement cannot expose or resurrect them. Complete private moves.
            for row in connection.execute("SELECT * FROM assets WHERE deleted_at IS NOT NULL").fetchall():
                self._retire_files(row)
            connection.commit()
        for directory in (self.root, self.originals, self.previews):
            _fsync_dir(directory)

    @contextmanager
    def _connect(self) -> Iterator[sqlite3.Connection]:
        connection = sqlite3.connect(self.database, timeout=self.config.database_timeout, isolation_level=None)
        try:
            connection.row_factory = sqlite3.Row
            connection.execute("PRAGMA synchronous=FULL")
            deadline = time.monotonic() + self.config.database_timeout
            connection.set_progress_handler(lambda: int(time.monotonic() >= deadline), 1000)
            yield connection
        finally:
            connection.close()

    @staticmethod
    def _asset(row: sqlite3.Row) -> dict:
        return {key: row[key] for key in ("id", "name", "width", "height", "bytes", "created_at")}

    def catalog(self) -> dict:
        try:
            with self._connect() as connection:
                connection.execute("BEGIN")
                rows = connection.execute("SELECT * FROM assets WHERE deleted_at IS NULL ORDER BY created_at,id LIMIT ?", (self.config.max_assets + 1,)).fetchall()
                if len(rows) > self.config.max_assets:
                    raise Fault(503, "素材库状态异常")
                hidden = [row[0] for row in connection.execute("SELECT id FROM hidden_builtins ORDER BY id")]
                if any(asset_id not in BUILTIN_IDS for asset_id in hidden) or len(hidden) > len(BUILTIN_IDS):
                    raise Fault(503, "素材库状态异常")
                return {"version": 1, "assets": [self._asset(row) for row in rows], "hidden_builtins": hidden}
        except sqlite3.Error:
            raise Fault(503, "素材库暂不可用") from None

    def _find(self, connection: sqlite3.Connection, owner: str, request_id: str) -> sqlite3.Row | None:
        return connection.execute("SELECT * FROM assets WHERE owner=? AND request_id=?", (owner, request_id)).fetchone()

    def _existing(self, row: sqlite3.Row, content_sha: str) -> dict:
        if row["content_sha"] != content_sha:
            raise Fault(409, "上传标识已用于其他文件")
        if row["deleted_at"] is not None:
            raise Fault(410, "素材已删除，请使用新上传标识")
        # Never silently replace damaged registered material under an immutable ID.
        try:
            for path, size in ((self.originals / (row["id"] + ".original"), row["original_bytes"]),
                               (self.previews / (row["id"] + ".webp"), row["bytes"])):
                info = path.lstat()
                if not stat.S_ISREG(info.st_mode) or info.st_size != size:
                    raise OSError()
        except OSError:
            raise Fault(503, "素材库状态异常") from None
        return {"asset": self._asset(row), "reused": True}

    def lookup_retry(self, owner: str, request_id: str, data: bytes) -> dict | None:
        try:
            with self._connect() as connection:
                row = self._find(connection, owner, request_id)
                return None if row is None else self._existing(row, hashlib.sha256(data).hexdigest())
        except sqlite3.Error:
            raise Fault(503, "素材库暂不可用") from None

    def _stage(self, data: bytes) -> Path:
        path = self.staging / (uuid.uuid4().hex + ".tmp")
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            with os.fdopen(fd, "wb") as output:
                output.write(data)
                output.flush()
                os.fsync(output.fileno())
        except BaseException:
            path.unlink(missing_ok=True)
            raise
        return path

    def commit(self, owner: str, request_id: str, name: str, original: bytes, preview: bytes) -> dict:
        asset_id = "upload-" + hashlib.sha256((owner + ":" + request_id).encode("ascii")).hexdigest()[:32]
        content_sha = hashlib.sha256(original).hexdigest()
        stages, installed = [], []
        commit_attempted = False
        rolled_back = False
        try:
            with self._connect() as connection:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    row = self._find(connection, owner, request_id)
                    if row is not None:
                        return self._existing(row, content_sha)
                    # Private recoverable trash consumes both item and byte
                    # budgets; deleting cannot enable unbounded disk/DB growth.
                    count, total = connection.execute("SELECT COUNT(*),COALESCE(SUM(original_bytes),0) FROM assets").fetchone()
                    if count >= self.config.max_assets or total + len(original) > self.config.max_original_bytes:
                        raise Fault(507, "素材库容量已满")
                    if connection.execute("SELECT 1 FROM assets WHERE id=?", (asset_id,)).fetchone():
                        raise Fault(409, "上传标识冲突")
                    stages.append(self._stage(original))
                    stages.append(self._stage(preview))
                    for staged, final in zip(stages, (self.originals / (asset_id + ".original"), self.previews / (asset_id + ".webp"))):
                        # Exclusive hardlink is atomic and cannot overwrite an existing ID.
                        os.link(staged, final, follow_symlinks=False)
                        installed.append(final)
                    _fsync_dir(self.originals)
                    _fsync_dir(self.previews)
                    created = datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
                    connection.execute("INSERT INTO assets (id,owner,request_id,content_sha,name,width,height,bytes,original_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", (
                        asset_id, owner, request_id, content_sha, name, 256, 256, len(preview), len(original), created))
                    row = self._find(connection, owner, request_id)
                    commit_attempted = True
                    connection.commit()
                    return {"asset": self._asset(row), "reused": False}
                except BaseException:
                    if connection.in_transaction:
                        # Unlink before releasing the SQLite write lock; a
                        # retry of this same ID must never race orphan cleanup.
                        try:
                            for path in installed:
                                path.unlink(missing_ok=True)
                            if installed:
                                _fsync_dir(self.originals)
                                _fsync_dir(self.previews)
                            installed.clear()
                        finally:
                            connection.rollback()
                            rolled_back = True
                    raise
        except Fault:
            raise
        except (sqlite3.Error, OSError):
            raise Fault(503, "素材保存失败，请使用原上传标识重试") from None
        finally:
            # A failed/uncertain COMMIT may already be durable: never delete its
            # files unless the transaction was definitely rolled back.
            if not commit_attempted or rolled_back:
                for path in installed:
                    path.unlink(missing_ok=True)
                if installed:
                    _fsync_dir(self.originals)
                    _fsync_dir(self.previews)
            for path in stages:
                path.unlink(missing_ok=True)

    def open_preview(self, asset_id: str):
        if not ASSET_RE.fullmatch(asset_id):
            raise Fault(404, "图片不存在")
        try:
            with self._connect() as connection:
                row = connection.execute("SELECT bytes FROM assets WHERE id=? AND deleted_at IS NULL", (asset_id,)).fetchone()
            if row is None:
                raise Fault(404, "图片不存在")
            fd = os.open(self.previews / (asset_id + ".webp"), os.O_RDONLY | os.O_NOFOLLOW)
            try:
                info = os.fstat(fd)
                if not stat.S_ISREG(info.st_mode) or info.st_size != row["bytes"]:
                    raise Fault(404, "图片不存在")
                return os.fdopen(fd, "rb"), info.st_size
            except BaseException:
                os.close(fd)
                raise
        except sqlite3.Error:
            raise Fault(503, "素材库暂不可用") from None
        except OSError:
            raise Fault(404, "图片不存在") from None

    def _retire_files(self, row: sqlite3.Row) -> None:
        for source_dir, trash_dir, suffix, size in (
                (self.originals, self.trash_originals, ".original", row["original_bytes"]),
                (self.previews, self.trash_previews, ".webp", row["bytes"])):
            source = source_dir / (row["id"] + suffix)
            destination = trash_dir / source.name
            if source.exists() or source.is_symlink():
                info = source.lstat()
                if not stat.S_ISREG(info.st_mode) or info.st_size != size:
                    raise OSError("unsafe private asset")
                try:
                    os.link(source, destination, follow_symlinks=False)
                except FileExistsError:
                    target = destination.lstat()
                    if not stat.S_ISREG(target.st_mode) or (target.st_dev, target.st_ino) != (info.st_dev, info.st_ino):
                        raise OSError("private trash collision") from None
                _fsync_dir(trash_dir)
                source.unlink(missing_ok=True)
                _fsync_dir(source_dir)
            else:
                target = destination.lstat()
                if not stat.S_ISREG(target.st_mode) or target.st_size != size:
                    raise OSError("missing private trash")

    def delete(self, asset_id: str) -> dict:
        kind = "builtin" if asset_id in BUILTIN_IDS else "uploaded"
        if kind == "uploaded" and not ASSET_RE.fullmatch(asset_id):
            raise Fault(404, "素材不存在")
        row = None
        try:
            with self._connect() as connection:
                connection.execute("BEGIN IMMEDIATE")
                created = datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z")
                if kind == "builtin":
                    connection.execute("INSERT INTO hidden_builtins (id,deleted_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING", (asset_id, created))
                else:
                    row = connection.execute("SELECT * FROM assets WHERE id=?", (asset_id,)).fetchone()
                    if row is None:
                        raise Fault(404, "素材不存在")
                    connection.execute("UPDATE assets SET deleted_at=? WHERE id=? AND deleted_at IS NULL", (created, asset_id))
                connection.commit()
                if row is not None:
                    # Hold the SQLite write lock again during retirement so a
                    # second process/restart cannot race this private file move.
                    connection.execute("BEGIN IMMEDIATE")
                    try:
                        self._retire_files(row)
                    finally:
                        connection.rollback()
            return {"id": asset_id, "kind": kind, "removed": True}
        except Fault:
            raise
        except (sqlite3.Error, OSError):
            raise Fault(503, "删除结果待确认，请重试核对") from None


class IconService:
    def __init__(self, config: Config, *, verifier: Callable[[str], Admin] | None = None,
                 converter: Callable[[bytes], bytes] = convert_image,
                 head_reader: Callable[[str], str] | None = None):
        config.validate()
        self.config = config
        # Dependency injection is explicit Python-only for isolated tests. The
        # runnable production CLI always constructs the real backend verifier.
        self.verifier = OpenListVerifier(config) if verifier is None else verifier
        self.head_reader = OpenListVerifier(config).read_head if head_reader is None else head_reader
        self.converter = converter
        self.store = CatalogStore(config)
        self._slots = threading.BoundedSemaphore(2)
        self._delete_slot = threading.BoundedSemaphore(1)
        self._pending_lock = threading.Lock()
        self._pending_deletes = set()

    @contextmanager
    def upload_slot(self):
        if not self._slots.acquire(blocking=False):
            raise Fault(429, "上传繁忙，请稍后重试")
        try:
            yield
        finally:
            self._slots.release()

    def save(self, authorization: str, admin: Admin, request_id: str, name: str, data: bytes) -> dict:
        if not REQUEST_RE.fullmatch(request_id):
            raise Fault(400, "上传标识无效")
        if not data or len(data) > self.config.max_input_bytes:
            raise Fault(413, "文件大小超出限制")
        existing = self.store.lookup_retry(admin.user_id, request_id, data)
        if existing is not None:
            with self._pending_lock:
                if existing["asset"]["id"] in self._pending_deletes:
                    raise Fault(409, "素材删除处理中，请稍后刷新")
            return existing
        preview = self.converter(data)
        # Check again immediately before staging/writing. Revocation or an
        # identity change during upload/conversion fails closed.
        current = self.verifier(authorization)
        if current != admin:
            raise Fault(403, "管理员身份已变更")
        return self.store.commit(admin.user_id, request_id, name, data, preview)

    def delete(self, authorization: str, admin: Admin, asset_id: str) -> dict:
        if asset_id not in BUILTIN_IDS and not ASSET_RE.fullmatch(asset_id):
            raise Fault(404, "素材不存在")
        if not self._delete_slot.acquire(blocking=False):
            raise Fault(429, "删除处理中，请稍后重试")
        with self._pending_lock:
            self._pending_deletes.add(asset_id)
        try:
            if asset_id in head_references(self.head_reader(authorization)):
                raise Fault(409, "正在使用，请先更换并保存")
            current = self.verifier(authorization)
            if current != admin:
                raise Fault(403, "管理员身份已变更")
            # Repeat the authoritative head read immediately before the local
            # tombstone transaction, not a client-supplied in-use declaration.
            if asset_id in head_references(self.head_reader(authorization)):
                raise Fault(409, "正在使用，请先更换并保存")
            return self.store.delete(asset_id)
        finally:
            with self._pending_lock:
                self._pending_deletes.discard(asset_id)
            self._delete_slot.release()
