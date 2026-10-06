"""Bounded loopback HTTP server; never a filesystem or fallback-HTML server."""
from __future__ import annotations

import http.client
import io
import ipaddress
import json
import re
import socket
import socketserver
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

from .core import Admin, BUILTIN_IDS, Fault, IconService, REQUEST_RE, decode_name, validate_authorization

API = "/icon-library/api/"
ASSET_PATH = re.compile(r"/icon-library/assets/(upload-[0-9a-f]{32})\.webp\Z", re.ASCII)
DELETE_PATH = re.compile(r"/icon-library/api/assets/([a-z0-9-]+)\Z", re.ASCII)
HEADER_NAME = re.compile(r"[!#$%&'*+\-.^_`|~0-9A-Za-z]+\Z", re.ASCII)
SAFE_ERRORS = {
    400: "请求无效", 401: "请先登录管理员账号", 403: "权限不足", 404: "资源不存在",
    405: "请求方法不允许", 408: "请求超时", 409: "上传标识冲突", 410: "素材已删除", 411: "缺少文件长度",
    413: "文件大小超出限制", 414: "请求路径过长", 415: "文件格式不支持",
    417: "不支持此请求预期", 422: "图片无效或损坏", 429: "服务繁忙，请稍后重试",
    431: "请求头过大", 500: "服务内部错误", 501: "请求方法不支持",
    502: "权限校验失败", 503: "服务暂不可用", 504: "权限校验超时",
    505: "HTTP 版本不支持", 507: "素材库容量已满",
}


class DeadlineReader(io.RawIOBase):
    """Absolute deadlines beat drip-feed slowloris, not just idle timeouts."""
    def __init__(self, connection: socket.socket, idle_timeout: float, budget: float):
        self.connection = connection
        self.idle_timeout = idle_timeout
        self.deadline = time.monotonic() + budget
        self.expired = False
        super().__init__()

    def readable(self) -> bool:
        return True

    def reset(self, budget: float) -> None:
        self.deadline = time.monotonic() + budget
        self.expired = False

    def readinto(self, buffer) -> int:
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            self.expired = True
            raise TimeoutError()
        self.connection.settimeout(min(remaining, self.idle_timeout))
        try:
            received = self.connection.recv_into(buffer)
        except socket.timeout:
            self.expired = True
            raise TimeoutError() from None
        if time.monotonic() >= self.deadline:
            self.expired = True
            raise TimeoutError()
        return received


class HeaderReader:
    def __init__(self, wrapped):
        self.wrapped, self.total = wrapped, 0

    def readline(self, limit=-1):
        line = self.wrapped.readline(min(8193, limit) if limit >= 0 else 8193)
        if not line:
            raise Fault(400, "请求头不完整")
        self.total += len(line)
        if len(line) > 8192 or self.total > 32768:
            raise http.client.LineTooLong("headers")
        if line and (not line.endswith(b"\r\n") or line.startswith((b" ", b"\t"))):
            raise Fault(400, "请求头无效")
        return line


class IconHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "IconService"
    sys_version = ""
    wbufsize = 0

    @property
    def app(self) -> IconService:
        return self.server.app

    def setup(self):
        super().setup()
        self.rfile.close()
        self._reader = DeadlineReader(self.connection, self.app.config.idle_timeout, self.app.config.header_timeout)
        self.rfile = io.BufferedReader(self._reader, buffer_size=8192)
        self._sent = False
        self._upload_headers = None

    def version_string(self):
        return "IconService"

    def log_message(self, format, *args):
        # Suppress request paths, Authorization, filenames and exception details.
        pass

    def log_error(self, format, *args):
        pass

    def send_error(self, code, message=None, explain=None):
        self.respond_fault(Fault(int(code), SAFE_ERRORS.get(int(code), "请求失败")))

    def _reply(self, status: int, data: dict, *, headers: dict | None = None):
        body = json.dumps(data, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.connection.settimeout(self.app.config.idle_timeout)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Connection", "close")
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self._sent = True
        self.close_connection = True
        if self.command != "HEAD":
            self.wfile.write(body)

    def respond_fault(self, error: Fault):
        extra = {"Retry-After": "1"} if error.status in (429, 503) else None
        self._reply(error.status, {"code": error.status, "message": error.message}, headers=extra)

    def _one_header(self, name: str, *, required: bool = False) -> str | None:
        values = self.headers.get_all(name, [])
        if len(values) > 1:
            raise Fault(400, "请求头重复")
        if not values:
            if required:
                raise Fault(400, "请求头缺失")
            return None
        return values[0]

    def _check_common_headers(self):
        if self.headers.defects:
            raise Fault(400, "请求头无效")
        for key, value in self.headers.items():
            if not HEADER_NAME.fullmatch(key) or any(ord(c) < 32 or ord(c) == 127 for c in value):
                raise Fault(400, "请求头无效")
        if self.request_version == "HTTP/1.1":
            host = self._one_header("Host", required=True)
            if not host or len(host) > 255 or any(c in host for c in "/\\@ "):
                raise Fault(400, "请求头无效")
        if self._one_header("Transfer-Encoding") is not None:
            raise Fault(400, "不支持分块请求")
        if self._one_header("Content-Encoding") is not None:
            raise Fault(415, "不支持压缩请求体")
        self._one_header("Authorization")
        self._one_header("Origin")
        self._one_header("Content-Length")
        self._one_header("Expect")

    def _prepare_upload(self):
        if self._upload_headers is not None:
            return self._upload_headers
        self._check_common_headers()
        if self._one_header("Origin") != self.app.config.origin:
            raise Fault(403, "上传来源不允许")
        authorization = validate_authorization(self._one_header("Authorization"))
        if self._one_header("Content-Type") != "application/octet-stream":
            raise Fault(415, "请使用原始文件上传")
        length = self._one_header("Content-Length")
        if length is None:
            raise Fault(411, "缺少文件长度")
        if not re.fullmatch(r"[0-9]{1,10}", length, flags=re.ASCII):
            raise Fault(400, "文件长度无效")
        length = int(length)
        if not 1 <= length <= self.app.config.max_input_bytes:
            raise Fault(413, "文件大小超出限制")
        request_id = self._one_header("X-Upload-Id", required=True)
        if not REQUEST_RE.fullmatch(request_id):
            raise Fault(400, "上传标识无效")
        name = decode_name(self._one_header("X-Icon-Name", required=True))
        expect = self._one_header("Expect")
        if expect is not None and expect.lower() != "100-continue":
            raise Fault(417, "不支持此请求预期")
        admin = self.app.verifier(authorization)
        if not isinstance(admin, Admin) or not re.fullmatch(r"[1-9][0-9]{0,19}", admin.user_id):
            raise Fault(502, "权限校验失败")
        self._upload_headers = authorization, admin, request_id, name, length
        return self._upload_headers

    def handle_expect_100(self):
        try:
            if self.command != "POST" or self.path != API + "upload":
                raise Fault(417, "不支持此请求预期")
            self._prepare_upload()
            self.send_response_only(100)
            self.end_headers()
            return True
        except Fault as error:
            self.respond_fault(error)
            return False

    def handle_one_request(self):
        # Single-request connections avoid body/pipelining ambiguity. The
        # request line is capped independently of the aggregate header cap.
        self.command, self.request_version, self.requestline = None, "HTTP/1.1", ""
        self.close_connection = True
        try:
            self.raw_requestline = self.rfile.readline(4097)
            if not self.raw_requestline:
                return
            if len(self.raw_requestline) > 4096:
                raise Fault(414, "请求路径过长")
            if not self.raw_requestline.endswith(b"\r\n"):
                raise Fault(400, "请求无效")
            line = self.raw_requestline[:-2]
            parts = line.split(b" ")
            if (len(parts) != 3 or not parts[0] or not parts[1]
                    or parts[2] not in (b"HTTP/1.0", b"HTTP/1.1")
                    or any(char < 33 or char > 126 for char in parts[0] + parts[1])):
                raise Fault(400, "请求无效")
            raw_path = parts[1].decode("ascii")
            if not raw_path.startswith("/") or raw_path.startswith("//"):
                raise Fault(400, "请求路径无效")
            buffered = self.rfile
            self.rfile = HeaderReader(buffered)
            try:
                if not self.parse_request():
                    return
            finally:
                self.rfile = buffered
            self.close_connection = True
            self._check_common_headers()
            if self.command == "POST":
                self.do_POST()
            elif self.command in ("GET", "HEAD"):
                self.do_GET()
            elif self.command == "DELETE":
                self.do_DELETE()
            else:
                raise Fault(405, "请求方法不允许")
            self.wfile.flush()
        except Fault as error:
            if not self._sent:
                try:
                    self.respond_fault(error)
                except OSError:
                    pass
        except (socket.timeout, TimeoutError):
            if not self._sent:
                try:
                    self.respond_fault(Fault(408, "请求超时"))
                except OSError:
                    pass
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            # A saved upload is still committed if the response is lost. Retry
            # the original request ID; never attempt a compensating deletion.
            pass
        except Exception:
            if not self._sent:
                try:
                    self.respond_fault(Fault(500, "服务内部错误"))
                except OSError:
                    pass
        finally:
            self.close_connection = True

    def do_GET(self):
        length = self._one_header("Content-Length")
        if length not in (None, "0") or self._one_header("Expect") is not None:
            raise Fault(400, "请求无效")
        if self.path == API + "catalog":
            self._reply(200, {"code": 200, "data": self.app.store.catalog()})
            return
        if self.path == API + "health":
            self._reply(200, {"code": 200, "data": {"version": 1}})
            return
        matched = ASSET_PATH.fullmatch(self.path)
        if not matched:
            raise Fault(404, "资源不存在")
        asset_id = matched[1]
        source, size = self.app.store.open_preview(asset_id)
        with source:
            self.connection.settimeout(self.app.config.idle_timeout)
            self.send_response(200)
            self.send_header("Content-Type", "image/webp")
            self.send_header("Content-Length", str(size))
            self.send_header("Cache-Control", "public, max-age=31536000, immutable")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("ETag", '"' + asset_id + '"')
            self.send_header("Connection", "close")
            self.end_headers()
            self._sent = True
            if self.command != "HEAD":
                deadline = time.monotonic() + 5.0
                while chunk := source.read(65536):
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise TimeoutError()
                    self.connection.settimeout(min(self.app.config.idle_timeout, remaining))
                    self.wfile.write(chunk)

    def do_POST(self):
        if self.path != API + "upload":
            raise Fault(404, "资源不存在")
        authorization, admin, request_id, name, length = self._prepare_upload()
        # Two slots cover both receiving and conversion, so slow bodies cannot
        # leave unbounded 5 MiB buffers while waiting for a converter.
        with self.app.upload_slot():
            self._reader.reset(self.app.config.body_timeout)
            body = bytearray()
            while len(body) < length:
                if time.monotonic() >= self._reader.deadline:
                    raise Fault(408, "请求超时")
                chunk = self.rfile.read1(min(65536, length - len(body)))
                if not chunk:
                    raise Fault(400, "文件内容不完整")
                body.extend(chunk)
            result = self.app.save(authorization, admin, request_id, name, bytes(body))
            self._reply(200, {"code": 200, "data": result})

    def do_DELETE(self):
        matched = DELETE_PATH.fullmatch(self.path)
        if not matched or (matched[1] not in BUILTIN_IDS and not re.fullmatch(r"upload-[0-9a-f]{32}", matched[1])):
            raise Fault(404, "素材不存在")
        if self._one_header("Origin") != self.app.config.origin:
            raise Fault(403, "删除来源不允许")
        authorization = validate_authorization(self._one_header("Authorization"))
        if self._one_header("Content-Length") not in (None, "0") or self._one_header("Expect") is not None:
            raise Fault(400, "删除请求不可携带内容")
        admin = self.app.verifier(authorization)
        if not isinstance(admin, Admin) or not re.fullmatch(r"[1-9][0-9]{0,19}", admin.user_id):
            raise Fault(502, "权限校验失败")
        result = self.app.delete(authorization, admin, matched[1])
        self._reply(200, {"code": 200, "data": result})


class IconHTTPServer(socketserver.ThreadingMixIn, HTTPServer):
    allow_reuse_address = True
    daemon_threads = False
    block_on_close = True
    request_queue_size = 16

    def __init__(self, address: tuple[str, int], app: IconService):
        if not ipaddress.ip_address(address[0]).is_loopback:
            raise ValueError("icon service must bind to loopback")
        self.app = app
        self._connections = threading.BoundedSemaphore(app.config.max_connections)
        super().__init__(address, IconHandler)

    def process_request(self, request, client_address):
        if not self._connections.acquire(blocking=False):
            body = json.dumps({"code": 503, "message": "服务繁忙，请稍后重试"}, ensure_ascii=False).encode("utf-8")
            reply = (b"HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json; charset=utf-8\r\n"
                     b"Connection: close\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\nRetry-After: 1\r\n"
                     + ("Content-Length: " + str(len(body)) + "\r\n\r\n").encode("ascii") + body)
            try:
                request.settimeout(0.25)
                request.sendall(reply)
            except OSError:
                pass
            finally:
                self.shutdown_request(request)
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self._connections.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self._connections.release()

    def handle_error(self, request, client_address):
        # Do not print tracebacks containing request headers or backend bodies.
        pass
