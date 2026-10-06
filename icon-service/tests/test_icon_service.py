"""Real Pillow/SQLite/socket tests; mock auth is isolated and explicitly named."""
from __future__ import annotations

import contextlib
import hashlib
import http.client
import io
import json
import os
import socket
import sqlite3
import struct
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import uuid
import zlib
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import quote
from unittest.mock import patch

from PIL import Image, PngImagePlugin

from icon_service.core import (
    Admin, BUILTIN_IDS, CatalogStore, Config, Fault, IconService, MAX_INPUT_BYTES,
    OpenListVerifier, convert_image, decode_name, head_references,
)
from icon_service.httpd import IconHTTPServer

SCRATCH = Path(os.environ.get("OPENLIST_ICON_TEST_SCRATCH", str(Path(tempfile.gettempdir()) / "openlist-icon-tests")))
SCRATCH.mkdir(mode=0o700, parents=True, exist_ok=True)
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
UPLOAD = "/icon-library/api/upload"
CATALOG = "/icon-library/api/catalog"
ORIGIN = "https://openlist.example"
# Deliberately synthetic tokens for isolated tests, never production logins.
TEST_ADMIN = "isolated-test-admin"
TEST_ADMIN_2 = "isolated-test-admin-2"


def image_bytes(format="PNG", size=(48, 24), color=(210, 40, 70, 128), **kwargs):
    image = Image.new("RGBA", size, color)
    if format == "JPEG":
        image = image.convert("RGB")
    stream = io.BytesIO()
    image.save(stream, format=format, **kwargs)
    return stream.getvalue()


def png_chunk(kind, payload):
    return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", zlib.crc32(kind + payload) & 0xffffffff)


def png_dimensions(width, height):
    return (PNG_SIGNATURE
            + png_chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
            + png_chunk(b"IDAT", zlib.compress(b"")) + png_chunk(b"IEND", b""))


class IsolatedMockVerifier:
    """Only constructed by tests. No production switch reaches this class."""
    def __init__(self):
        self.calls = []
        self.lock = threading.Lock()

    def __call__(self, token):
        with self.lock:
            self.calls.append(token)
        if token == TEST_ADMIN:
            return Admin("7")
        if token == TEST_ADMIN_2:
            return Admin("9")
        if token == "isolated-test-api-failure":
            raise Fault(502, "权限校验失败")
        if token == "isolated-test-api-timeout":
            raise Fault(504, "权限校验超时")
        raise Fault(403, "仅启用的管理员可上传")


class RunningServer:
    def __init__(self, app):
        self.app = app
        self.server = IconHTTPServer(("127.0.0.1", 0), app)
        self.port = self.server.server_port
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.02}, name="isolated-icon-http")

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *exc):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
        if self.thread.is_alive():
            raise AssertionError("test server did not terminate")

    def request(self, method="GET", path=CATALOG, body=None, headers=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=6)
        try:
            connection.request(method, path, body=body, headers=headers or {})
            response = connection.getresponse()
            content = response.read()
            return response.status, dict(response.getheaders()), content
        finally:
            connection.close()

    def upload(self, data=None, request_id=None, name="图标.png", token=TEST_ADMIN, overrides=None, omit=()):
        if data is None:
            data = image_bytes()
        headers = {
            "Origin": ORIGIN, "Authorization": token,
            "Content-Type": "application/octet-stream", "Content-Length": str(len(data)),
            "X-Icon-Name": quote(name, safe="~()*!.'-"), "X-Upload-Id": request_id or uuid.uuid4().hex,
        }
        headers.update(overrides or {})
        for key in omit:
            headers.pop(key, None)
        return self.request("POST", UPLOAD, data, headers)

    def raw(self, request_bytes, *, half_close=False):
        with socket.create_connection(("127.0.0.1", self.port), timeout=5) as connection:
            connection.sendall(request_bytes)
            if half_close:
                connection.shutdown(socket.SHUT_WR)
            parts = []
            while True:
                try:
                    chunk = connection.recv(65536)
                except ConnectionResetError:
                    break
                if not chunk:
                    break
                parts.append(chunk)
            return b"".join(parts)

    def delete(self, asset_id, token=TEST_ADMIN, overrides=None, omit=()):
        headers = {"Origin": ORIGIN, "Authorization": token}
        headers.update(overrides or {})
        for key in omit:
            headers.pop(key, None)
        return self.request("DELETE", "/icon-library/api/assets/" + asset_id, headers=headers)


def upload_raw_headers(*extra, request_id=None, length=None):
    lines = [
        "POST " + UPLOAD + " HTTP/1.1", "Host: localhost", "Origin: " + ORIGIN,
        "Authorization: " + TEST_ADMIN, "Content-Type: application/octet-stream",
        "X-Icon-Name: test.png", "X-Upload-Id: " + (request_id or uuid.uuid4().hex),
    ]
    if length is not None:
        lines.append("Content-Length: " + str(length))
    return ("\r\n".join(lines + list(extra)) + "\r\n\r\n").encode("ascii")


class FixtureTest(unittest.TestCase):
    def setUp(self):
        # All fixtures under the configured test scratch, never real service data.
        self.tmp = tempfile.TemporaryDirectory(prefix="icon-service-tests-", dir=SCRATCH)
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name) / "data"
        self.config = Config(root=self.root)
        self.verifier = IsolatedMockVerifier()
        self.app = IconService(self.config, verifier=self.verifier)

    def running(self, app=None):
        return RunningServer(app or self.app)

    def response_json(self, response, expected=200):
        status, headers, body = response
        self.assertEqual(status, expected, body)
        self.assertEqual(headers["Content-Type"], "application/json; charset=utf-8")
        self.assertEqual(headers["X-Content-Type-Options"], "nosniff")
        self.assertNotIn("Access-Control-Allow-Origin", headers)
        data = json.loads(body)
        self.assertEqual(data["code"], expected)
        if expected != 200:
            self.assertEqual(set(data), {"code", "message"})
            self.assertTrue(data["message"])
            self.assertNotIn(TEST_ADMIN, body.decode())
        return data

    def raw_json(self, raw, expected):
        prefix, body = raw.split(b"\r\n\r\n", 1)
        self.assertIn((" " + str(expected) + " ").encode(), prefix.split(b"\r\n", 1)[0], raw)
        self.assertNotIn(b"Access-Control-Allow", prefix)
        value = json.loads(body)
        self.assertEqual(value["code"], expected)
        self.assertEqual(set(value), {"code", "message"})
        return value


class PNGTests(FixtureTest):
    def test_png_signature_real_decode_and_lossless_alpha(self):
        data = image_bytes(size=(400, 200))
        self.assertEqual(data[:8], PNG_SIGNATURE)
        with self.running() as server:
            result = self.response_json(server.upload(data))["data"]
            self.assertFalse(result["reused"])
            asset = result["asset"]
            self.assertEqual(set(asset), {"id", "name", "width", "height", "bytes", "created_at"})
            self.assertRegex(asset["id"], r"^upload-[0-9a-f]{32}$")
            self.assertEqual(asset["name"], "图标.png")
            status, headers, preview = server.request(path="/icon-library/assets/" + asset["id"] + ".webp")
            self.assertEqual(status, 200)
            self.assertEqual(headers["Content-Type"], "image/webp")
            self.assertIn("immutable", headers["Cache-Control"])
            self.assertEqual(headers["X-Content-Type-Options"], "nosniff")
            self.assertEqual(len(preview), asset["bytes"])
            with Image.open(io.BytesIO(preview)) as image:
                self.assertEqual(image.format, "WEBP")
                self.assertEqual(image.size, (256, 256))
                self.assertEqual(image.getchannel("A").getbbox(), (12, 70, 244, 186))
                reference = Image.new("RGBA", (400, 200), (210, 40, 70, 128))
                reference.thumbnail((232, 232), Image.Resampling.LANCZOS)
                # Resampling may round RGB of translucent pixels; lossless WebP
                # must preserve the actual resampled RGBA, not invent fidelity.
                self.assertEqual(image.getpixel((128, 128)), reference.getpixel((116, 58)))
                self.assertEqual(image.getpixel((0, 0))[3], 0)
            original = self.root / "originals" / (asset["id"] + ".original")
            self.assertEqual(original.read_bytes(), data)
            self.assertEqual(len(self.verifier.calls), 2)
            catalog = self.response_json(server.request())["data"]
            self.assertEqual(catalog, {"version": 1, "assets": [asset], "hidden_builtins": []})
            status, headers, body = server.request("HEAD", "/icon-library/assets/" + asset["id"] + ".webp")
            self.assertEqual(status, 200)
            self.assertEqual(body, b"")
            self.assertEqual(int(headers["Content-Length"]), asset["bytes"])

    def test_png_exif_rotation_and_metadata_stripped(self):
        exif = Image.Exif()
        exif[274] = 6
        exif[270] = "must-not-publish"
        text = PngImagePlugin.PngInfo()
        text.add_text("private", "must-not-publish")
        data = image_bytes(size=(400, 200), color=(100, 180, 40, 255), exif=exif, pnginfo=text)
        converted = convert_image(data)
        with Image.open(io.BytesIO(converted)) as image:
            self.assertEqual(image.getchannel("A").getbbox(), (70, 12, 186, 244))
            self.assertFalse(image.getexif())
            self.assertNotIn("private", image.info)
            self.assertNotIn("exif", image.info)
            self.assertNotIn("icc_profile", image.info)
        self.assertNotIn(b"must-not-publish", converted)

    def test_tiny_png_is_centered_without_upscale(self):
        with Image.open(io.BytesIO(convert_image(image_bytes(size=(8, 4))))) as image:
            self.assertEqual(image.getchannel("A").getbbox(), (124, 126, 132, 130))
            self.assertEqual(image.getpixel((128, 128)), (210, 40, 70, 128))

    def test_real_jpeg_webp_ico_gif_svg_renamed_png_are_rejected(self):
        fixtures = {fmt: image_bytes(format=fmt, size=(64, 64)) for fmt in ("JPEG", "WEBP", "ICO", "GIF")}
        fixtures["SVG"] = b'<svg xmlns="http://www.w3.org/2000/svg"><script>bad()</script></svg>'
        fixtures["HTML"] = b"<!doctype html><script>alert(1)</script>"
        with self.running() as server:
            for kind, data in fixtures.items():
                with self.subTest(kind=kind):
                    self.response_json(server.upload(data, name="renamed.png"), 415)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.assertEqual(list((self.root / "originals").iterdir()), [])

    def test_png_with_wrong_extension_is_rejected_and_uppercase_png_allowed(self):
        with self.running() as server:
            self.response_json(server.upload(name="image.jpg"), 415)
            self.response_json(server.upload(name="image"), 415)
            self.response_json(server.upload(name="中文.PNG"), 200)

    def test_apng_two_frames_and_one_frame_actl_are_rejected(self):
        stream = io.BytesIO()
        Image.new("RGBA", (32, 32), "red").save(stream, format="PNG", save_all=True,
            append_images=[Image.new("RGBA", (32, 32), "blue")], duration=[50, 50], loop=0)
        plain = image_bytes()
        single_frame_actl = plain[:33] + png_chunk(b"acTL", struct.pack(">II", 1, 0)) + plain[33:]
        with self.running() as server:
            for data in (stream.getvalue(), single_frame_actl):
                with self.subTest(bytes=len(data)):
                    self.response_json(server.upload(data), 415)

    def test_fake_png_signature_truncation_crc_and_appended_payload_fail(self):
        plain = image_bytes()
        malformed = [PNG_SIGNATURE + b"bad", plain[:30], plain[:-12], plain + b"<script>bad</script>"]
        bad_crc = bytearray(plain)
        bad_crc[29] ^= 1
        malformed.append(bytes(bad_crc))
        with self.running() as server:
            for index, data in enumerate(malformed):
                with self.subTest(index=index):
                    self.response_json(server.upload(data), 422)

    def test_dimensions_pixel_limit_and_bomb_fail_closed(self):
        fixtures = [(4097, 1), (1, 4097), (4001, 4000), (100000, 100000)]
        with self.running() as server:
            for width, height in fixtures:
                with self.subTest(size=(width, height)):
                    self.response_json(server.upload(png_dimensions(width, height)), 413)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])

    def test_five_mib_limit_before_body_and_convert(self):
        with self.running() as server:
            raw = server.raw(upload_raw_headers(length=MAX_INPUT_BYTES + 1))
            self.raw_json(raw, 413)
            self.assertEqual(self.verifier.calls, [])
        with self.assertRaises(Fault) as error:
            convert_image(b"x" * (MAX_INPUT_BYTES + 1))
        self.assertEqual(error.exception.status, 413)


class AuthAndHeaderTests(FixtureTest):
    def test_missing_empty_guest_disabled_and_api_failure_are_denied(self):
        with self.running() as server:
            for token, status in (("", 401), ("isolated-test-guest", 403), ("isolated-test-disabled", 403),
                                  ("isolated-test-api-failure", 502), ("isolated-test-api-timeout", 504)):
                with self.subTest(token=token):
                    self.response_json(server.upload(token=token), status)
            self.response_json(server.upload(omit=("Authorization",)), 401)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.assertEqual(list((self.root / "originals").iterdir()), [])

    def test_origin_exact_missing_null_and_foreign_no_cors(self):
        with self.running() as server:
            self.response_json(server.upload(omit=("Origin",)), 403)
            for origin in ("null", "https://openlist.example/", "http://openlist.example", "https://evil.example",
                           "https://openlist.example.evil.example"):
                with self.subTest(origin=origin):
                    self.response_json(server.upload(overrides={"Origin": origin}), 403)
            self.response_json(server.request("OPTIONS", UPLOAD), 405)
            self.assertEqual(self.verifier.calls, [])

    def test_raw_headers_length_content_type_encoding_and_duplicates(self):
        with self.running() as server:
            cases = [
                (upload_raw_headers(), 411),
                (upload_raw_headers(length=-1), 400),
                (upload_raw_headers(length="1,1"), 400),
                (upload_raw_headers(length="1e3"), 400),
                (upload_raw_headers(length=0), 413),
                (upload_raw_headers("Transfer-Encoding: chunked", length=1), 400),
                (upload_raw_headers("Content-Encoding: gzip", length=1), 415),
                (upload_raw_headers("Content-Length: 1", length=1), 400),
                (upload_raw_headers("Authorization: other", length=1), 400),
                (upload_raw_headers("Origin: " + ORIGIN, length=1), 400),
                (upload_raw_headers("X-Upload-Id: " + uuid.uuid4().hex, length=1), 400),
                (upload_raw_headers("X-Icon-Name: other.png", length=1), 400),
                (upload_raw_headers("Expect: unsupported", length=1), 417),
                (upload_raw_headers("X-Other: one\r\n two", length=1), 400),
            ]
            for request, expected in cases:
                with self.subTest(request=request):
                    self.raw_json(server.raw(request), expected)
            self.response_json(server.upload(overrides={"Content-Type": "image/png"}), 415)
            self.response_json(server.upload(overrides={"Content-Type": "application/octet-stream; charset=utf-8"}), 415)

    def test_upload_key_literal_format_never_repaired(self):
        with self.running() as server:
            for request_id in ("A" * 32, "f" * 31, "f" * 33, "g" * 32, "../" + "f" * 29, "0-" * 16):
                with self.subTest(request_id=request_id):
                    self.response_json(server.upload(request_id=request_id), 400)

    def test_filename_utf8_plain_path_controls_and_bad_percent(self):
        with self.running() as server:
            for name in ("../evil.png", "a/b.png", "a\\b.png", "evil\x00.png", "evil\r\n.png", "\u202eevil.png"):
                with self.subTest(name=repr(name)):
                    self.response_json(server.upload(name=name), 400)
            for encoded in ("%ZZ.png", "%ff.png", "%C0%AE.png", "%", "x" * 4000):
                with self.subTest(encoded=encoded[:40]):
                    self.response_json(server.upload(overrides={"X-Icon-Name": encoded}), 400)
            asset = self.response_json(server.upload(name="<script>alert(1)</script>.png"), 400)
            # '<' is plain JSON text, not executable markup; slash paths still fail.
            asset = self.response_json(server.upload(name="<img onerror=bad()>.png"))["data"]["asset"]
            self.assertEqual(asset["name"], "<img onerror=bad()>.png")

    def test_headers_request_line_oversize_and_unknown_paths_json(self):
        with self.running() as server:
            self.raw_json(server.raw(b"GET /" + b"x" * 5000 + b" HTTP/1.1\r\n\r\n"), 414)
            self.raw_json(server.raw(b"GET / HTTP/1.1\r\nHost: x\r\nX: " + b"a" * 9000 + b"\r\n\r\n"), 431)
            many = b"GET / HTTP/1.1\r\nHost: x\r\n" + (b"X: " + b"a" * 4000 + b"\r\n") * 9 + b"\r\n"
            self.raw_json(server.raw(many), 431)
            self.raw_json(server.raw(b"GET / HTTP/1.1\r\nHost: x\r\nHost: y\r\n\r\n"), 400)
            self.raw_json(server.raw(b"GET / HTTP/1.1\r\n\r\n"), 400)
            self.raw_json(server.raw(b"GET http://other/ HTTP/1.1\r\nHost: x\r\n\r\n"), 400)
            self.raw_json(server.raw(b"GET //icon-library/api/health HTTP/1.1\r\nHost: x\r\n\r\n"), 400)
            for path in ("/", "/icon-library/api/catalog?x=1", "/icon-library/api/catalog.db", "/icon-library/originals/a.png"):
                with self.subTest(path=path):
                    self.response_json(server.request(path=path), 404)

    def test_expect_100_is_only_sent_after_admin_check(self):
        with self.running() as server:
            denied = upload_raw_headers("Expect: 100-continue", length=20).replace(TEST_ADMIN.encode(), b"isolated-test-guest")
            raw = server.raw(denied)
            self.assertNotIn(b"100 Continue", raw)
            self.raw_json(raw, 403)
            data = image_bytes()
            with socket.create_connection(("127.0.0.1", server.port), timeout=5) as connection:
                connection.sendall(upload_raw_headers("Expect: 100-continue", length=len(data)))
                interim = connection.recv(65536)
                self.assertEqual(interim, b"HTTP/1.1 100 Continue\r\n\r\n")
                connection.sendall(data)
                raw = bytearray()
                while chunk := connection.recv(65536):
                    raw.extend(chunk)
                self.assertIn(b" 200 ", bytes(raw).split(b"\r\n", 1)[0])
                self.assertEqual(json.loads(bytes(raw).split(b"\r\n\r\n", 1)[1])["code"], 200)

    def test_permission_revoked_or_identity_changed_before_disk_write(self):
        for outcome in (Fault(403, "权限不足"), Admin("9")):
            with self.subTest(outcome=outcome):
                calls = []

                def revokes(token):
                    calls.append(token)
                    if len(calls) == 1:
                        return Admin("7")
                    if isinstance(outcome, Fault):
                        raise outcome
                    return outcome

                app = IconService(replace(self.config, root=Path(self.tmp.name) / uuid.uuid4().hex), verifier=revokes)
                with self.running(app) as server:
                    self.response_json(server.upload(), 403)
                    self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
                self.assertEqual(list(app.store.originals.iterdir()), [])


class PersistenceTests(FixtureTest):
    def test_idempotency_content_conflict_new_ids_preserved_and_owner_isolated(self):
        key, first, second = uuid.uuid4().hex, image_bytes(), image_bytes(color=(1, 2, 3, 255))
        with self.running() as server:
            one = self.response_json(server.upload(first, key))["data"]
            retry = self.response_json(server.upload(first, key, name="重命名.png"))["data"]
            self.assertTrue(retry["reused"])
            self.assertEqual(retry["asset"], one["asset"])
            self.response_json(server.upload(second, key), 409)
            two = self.response_json(server.upload(first, uuid.uuid4().hex))["data"]
            three = self.response_json(server.upload(first, key, token=TEST_ADMIN_2))["data"]
            self.assertEqual(len({one["asset"]["id"], two["asset"]["id"], three["asset"]["id"]}), 3)
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 3)
            self.assertEqual(one["asset"]["id"], "upload-" + hashlib.sha256(("7:" + key).encode()).hexdigest()[:32])
            self.assertEqual(len(list(self.app.store.originals.iterdir())), 3)
            self.assertEqual(len(list(self.app.store.previews.iterdir())), 3)
            self.assertEqual(list(self.app.store.staging.iterdir()), [])
        with contextlib.closing(sqlite3.connect(self.root / "catalog.db")) as database:
            rows = database.execute("SELECT owner,request_id,content_sha FROM assets").fetchall()
            self.assertEqual(len(rows), 3)
            self.assertEqual(database.execute("PRAGMA journal_mode").fetchone()[0], "wal")
            self.assertEqual(database.execute("PRAGMA integrity_check").fetchone()[0], "ok")

    def test_simultaneous_same_key_has_single_durable_asset(self):
        key = uuid.uuid4().hex
        barrier = threading.Barrier(2)
        app = IconService(self.config, verifier=self.verifier, converter=lambda data: (barrier.wait(timeout=5), convert_image(data))[1])
        with self.running(app) as server:
            with ThreadPoolExecutor(max_workers=2) as pool:
                responses = list(pool.map(lambda _: server.upload(request_id=key), range(2)))
            values = [self.response_json(value)["data"] for value in responses]
            self.assertEqual(sum(not value["reused"] for value in values), 1)
            self.assertEqual(len({value["asset"]["id"] for value in values}), 1)
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)
            self.assertEqual(len(list(app.store.originals.iterdir())), 1)
            self.assertEqual(list(app.store.staging.iterdir()), [])

    def test_simultaneous_conflicting_key_does_not_overwrite(self):
        key = uuid.uuid4().hex
        barrier = threading.Barrier(2)
        app = IconService(self.config, verifier=self.verifier, converter=lambda data: (barrier.wait(timeout=5), convert_image(data))[1])
        images = [image_bytes(color=(255, 0, 0, 255)), image_bytes(color=(0, 0, 255, 255))]
        with self.running(app) as server:
            with ThreadPoolExecutor(max_workers=2) as pool:
                responses = list(pool.map(lambda data: server.upload(data, key), images))
            self.assertEqual(sorted(value[0] for value in responses), [200, 409])
            for response in responses:
                self.response_json(response, response[0])
            asset = self.response_json(server.request())["data"]["assets"][0]
            winning = images[[response[0] for response in responses].index(200)]
            self.assertEqual((app.store.originals / (asset["id"] + ".original")).read_bytes(), winning)

    def test_two_conversion_slots_third_request_busy_then_retry_works(self):
        entered, release = threading.Event(), threading.Event()
        lock = threading.Lock()
        state = {"active": 0, "peak": 0, "calls": 0}

        def held(data):
            with lock:
                state["active"] += 1
                state["calls"] += 1
                state["peak"] = max(state["peak"], state["active"])
                if state["active"] == 2:
                    entered.set()
            try:
                if not release.wait(5):
                    raise AssertionError("converter release not signalled")
                return convert_image(data)
            finally:
                with lock:
                    state["active"] -= 1

        app = IconService(self.config, verifier=self.verifier, converter=held)
        with self.running(app) as server, ThreadPoolExecutor(max_workers=2) as pool:
            pending = [pool.submit(server.upload) for _ in range(2)]
            try:
                self.assertTrue(entered.wait(5))
                retry_key = uuid.uuid4().hex
                self.response_json(server.upload(request_id=retry_key), 429)
            finally:
                release.set()
            for future in pending:
                self.response_json(future.result())
            self.response_json(server.upload(request_id=retry_key))
        self.assertEqual(state["peak"], 2)
        self.assertEqual(state["calls"], 3)

    def test_quota_count_exact_200_and_retry_at_limit(self):
        data = image_bytes(size=(1, 1))
        preview = convert_image(data)
        for index in range(200):
            self.app.store.commit("7", f"{index:032x}", "相同名字.png", data, preview)
        with self.running() as server:
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 200)
            self.response_json(server.upload(data, f"{200:032x}"), 507)
            retry = self.response_json(server.upload(data, f"{0:032x}"))["data"]
            self.assertTrue(retry["reused"])
            self.assertEqual(len(list(self.app.store.originals.iterdir())), 200)

    def test_quota_original_bytes_exact_boundary_and_concurrent_reservation(self):
        data = image_bytes()
        config = replace(self.config, max_original_bytes=len(data) * 2)
        app = IconService(config, verifier=self.verifier)
        with self.running(app) as server:
            self.response_json(server.upload(data))
            self.response_json(server.upload(data))
            self.response_json(server.upload(data), 507)
            with contextlib.closing(sqlite3.connect(app.store.database)) as database:
                self.assertEqual(database.execute("SELECT SUM(original_bytes) FROM assets").fetchone()[0], len(data) * 2)
        config = replace(config, root=Path(self.tmp.name) / "quota-race", max_assets=1)
        barrier = threading.Barrier(2)
        app = IconService(config, verifier=self.verifier, converter=lambda data: (barrier.wait(timeout=5), convert_image(data))[1])
        with self.running(app) as server, ThreadPoolExecutor(max_workers=2) as pool:
            responses = list(pool.map(lambda _: server.upload(data), range(2)))
            self.assertEqual(sorted(response[0] for response in responses), [200, 507])
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)
            self.assertEqual(len(list(app.store.originals.iterdir())), 1)
            self.assertEqual(list(app.store.staging.iterdir()), [])

    def test_cold_restart_catalog_and_orphan_recovery_preserves_registered_originals(self):
        key, data = uuid.uuid4().hex, image_bytes()
        with self.running() as server:
            asset = self.response_json(server.upload(data, key))["data"]["asset"]
        orphan = "upload-" + "a" * 32
        (self.app.store.originals / (orphan + ".original")).write_bytes(b"uncommitted")
        (self.app.store.previews / (orphan + ".webp")).write_bytes(b"uncommitted")
        # Fresh process reads SQLite and real WebP from disk, not object state.
        code = ("import sys,json; from pathlib import Path; from icon_service.core import CatalogStore,Config; "
                "s=CatalogStore(Config(root=Path(sys.argv[1]))); print(json.dumps(s.catalog(),ensure_ascii=False))")
        process = subprocess.run([sys.executable, "-c", code, str(self.root)], cwd=Path(__file__).resolve().parents[1],
                                 text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10, check=True)
        self.assertEqual(json.loads(process.stdout), {"version": 1, "assets": [asset], "hidden_builtins": []})
        self.assertEqual(process.stderr, "")
        self.assertFalse((self.app.store.originals / (orphan + ".original")).exists())
        self.assertFalse((self.app.store.previews / (orphan + ".webp")).exists())
        app = IconService(self.config, verifier=self.verifier)
        with self.running(app) as server:
            self.assertTrue(self.response_json(server.upload(data, key))["data"]["reused"])
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)
            status, headers, body = server.request(path="/icon-library/assets/" + asset["id"] + ".webp")
            self.assertEqual(status, 200)
            with Image.open(io.BytesIO(body)) as image:
                image.load()
                self.assertEqual(image.size, (256, 256))
        self.assertEqual((app.store.originals / (asset["id"] + ".original")).read_bytes(), data)

    def test_lost_http_response_is_retryable_after_commit(self):
        key, data = uuid.uuid4().hex, image_bytes()
        with self.running() as server:
            with socket.create_connection(("127.0.0.1", server.port), timeout=5) as connection:
                connection.sendall(upload_raw_headers(request_id=key, length=len(data)) + data)
                # Close/reset without waiting for an upload response.
                connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
            deadline = time.monotonic() + 3
            while not self.app.store.catalog()["assets"] and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertEqual(len(self.app.store.catalog()["assets"]), 1)
            result = self.response_json(server.upload(data, key))["data"]
            self.assertTrue(result["reused"])
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)

    def test_file_install_failure_rolls_back_and_no_token_logging(self):
        with self.running() as server:
            stdout, stderr = io.StringIO(), io.StringIO()
            with patch("icon_service.core.os.link", side_effect=OSError("SECRET-token-dont-log")), contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                self.response_json(server.upload(), 503)
            self.assertEqual(stdout.getvalue(), "")
            self.assertEqual(stderr.getvalue(), "")
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.assertEqual(list(self.app.store.originals.iterdir()), [])
            self.assertEqual(list(self.app.store.previews.iterdir()), [])
            self.assertEqual(list(self.app.store.staging.iterdir()), [])
            self.response_json(server.upload())

    def test_sqlite_write_lock_wait_is_bounded_and_error_safe(self):
        app = IconService(replace(self.config, database_timeout=0.15), verifier=self.verifier)
        with self.running(app) as server, contextlib.closing(sqlite3.connect(app.store.database)) as database:
            database.execute("BEGIN IMMEDIATE")
            started = time.monotonic()
            self.response_json(server.upload(), 503)
            self.assertLess(time.monotonic() - started, 1)
            database.rollback()
            self.response_json(server.upload())
            self.assertEqual(list(app.store.staging.iterdir()), [])


class PublicAndTransportTests(FixtureTest):
    def test_only_registered_previews_never_private_files_or_html_fallback(self):
        fake = "upload-" + "1" * 32
        (self.app.store.previews / (fake + ".webp")).write_bytes(convert_image(image_bytes()))
        with self.running() as server:
            self.response_json(server.request(path="/icon-library/assets/" + fake + ".webp"), 404)
            for path in ("/icon-library/assets/../catalog.db", "/icon-library/assets/%2e%2e/catalog.db",
                         "/icon-library/assets/upload-" + "F" * 32 + ".webp",
                         "/icon-library/assets/upload-" + "1" * 32 + ".webp?x=1",
                         "/icon-library/originals/" + fake + ".original", "/icon-library/catalog.db",
                         "/icon-library/.staging/a.tmp", "/icon-library/assets/catalog.db"):
                with self.subTest(path=path):
                    self.response_json(server.request(path=path), 404)
            status, headers, body = server.request("HEAD", "/icon-library/assets/" + fake + ".webp")
            self.assertEqual(status, 404)
            self.assertEqual(body, b"")
            self.assertIn("application/json", headers["Content-Type"])
            self.response_json(server.request(path="/icon-library/api/health"))
            self.assertEqual(self.verifier.calls, [])

    def test_symlink_registered_preview_is_not_followed(self):
        with self.running() as server:
            asset = self.response_json(server.upload())["data"]["asset"]
            path = self.app.store.previews / (asset["id"] + ".webp")
            private = Path(self.tmp.name) / "private-secret"
            private.write_bytes(b"SECRET")
            path.unlink()
            path.symlink_to(private)
            self.response_json(server.request(path="/icon-library/assets/" + asset["id"] + ".webp"), 404)

    def test_slow_body_idle_partial_and_absolute_deadline(self):
        config = replace(self.config, header_timeout=0.4, body_timeout=0.3, idle_timeout=0.2)
        app = IconService(config, verifier=self.verifier)
        with self.running(app) as server:
            started = time.monotonic()
            self.raw_json(server.raw(upload_raw_headers(length=100)), 408)
            self.assertLess(time.monotonic() - started, 1)
            self.raw_json(server.raw(upload_raw_headers(length=100) + b"x", half_close=True), 400)
            with socket.create_connection(("127.0.0.1", server.port), timeout=3) as connection:
                connection.sendall(upload_raw_headers(length=100))
                started = time.monotonic()
                for _ in range(6):
                    try:
                        connection.sendall(b"x")
                    except OSError:
                        break
                    time.sleep(0.08)
                raw = bytearray()
                while chunk := connection.recv(65536):
                    raw.extend(chunk)
                self.raw_json(bytes(raw), 408)
                self.assertLess(time.monotonic() - started, 1)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])

    def test_slow_headers_total_deadline_even_dripping_under_idle_limit(self):
        app = IconService(replace(self.config, header_timeout=0.3, idle_timeout=0.2), verifier=self.verifier)
        with self.running(app) as server:
            started = time.monotonic()
            with socket.create_connection(("127.0.0.1", server.port), timeout=3) as connection:
                for chunk in (b"GET ", b"/icon", b"-lib", b"rary", b"/api", b"/health "):
                    try:
                        connection.sendall(chunk)
                    except OSError:
                        break
                    time.sleep(0.08)
                raw = bytearray()
                while chunk := connection.recv(65536):
                    raw.extend(chunk)
                self.raw_json(bytes(raw), 408)
            self.assertLess(time.monotonic() - started, 1)

    def test_connection_count_bounded_and_overload_json(self):
        app = IconService(replace(self.config, max_connections=2, header_timeout=1, idle_timeout=1), verifier=self.verifier)
        with self.running(app) as server:
            connections = []
            try:
                for _ in range(2):
                    connection = socket.create_connection(("127.0.0.1", server.port), timeout=2)
                    connections.append(connection)
                    connection.sendall(b"GET ")
                deadline = time.monotonic() + 0.7
                while app is not None and server.server._connections._value != 0 and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertEqual(server.server._connections._value, 0)
                self.response_json(server.request(path="/icon-library/api/health"), 503)
            finally:
                for connection in connections:
                    connection.close()

    def test_loopback_bind_and_config_cannot_disable_auth_or_raise_limits(self):
        self.assertEqual(Config().root, Path("/var/lib/openlist-icons"))
        self.assertEqual(Config().backend, "http://127.0.0.1:5244")
        self.assertEqual(Config().origin, ORIGIN)
        with self.assertRaises(ValueError):
            IconHTTPServer(("0.0.0.0", 0), self.app)
        for backend in ("https://example.com", "http://127.0.0.1:5244/path", "http://u:p@127.0.0.1:5244",
                        "http://localhost:5244", "http://10.0.0.1:5244"):
            with self.subTest(backend=backend), self.assertRaises(ValueError):
                replace(self.config, backend=backend).validate()
        for invalid in (replace(self.config, max_input_bytes=MAX_INPUT_BYTES + 1),
                        replace(self.config, max_assets=201), replace(self.config, max_original_bytes=256 * 1024 * 1024 + 1)):
            with self.assertRaises(ValueError):
                invalid.validate()
        with patch.dict(os.environ, {"OPENLIST_ICONS_ROOT": str(Path(self.tmp.name) / "env-auth"),
                                     "OPENLIST_ICONS_SKIP_AUTH": "1", "OPENLIST_ICONS_ADMIN_TOKEN": TEST_ADMIN}):
            production = IconService(Config.from_env())
            self.assertIsInstance(production.verifier, OpenListVerifier)

    def test_catalog_timeout_is_bounded_under_exclusive_database_lock(self):
        app = IconService(replace(self.config, database_timeout=0.12), verifier=self.verifier)
        with contextlib.closing(sqlite3.connect(app.store.database)) as database, self.running(app) as server:
            # WAL readers ordinarily do not wait for a writer. Switching only
            # this isolated fixture to DELETE forces a genuine blocking read.
            database.execute("PRAGMA journal_mode=DELETE")
            database.execute("BEGIN EXCLUSIVE")
            started = time.monotonic()
            self.response_json(server.request(), 503)
            self.assertLess(time.monotonic() - started, 0.8)
            database.rollback()
            self.response_json(server.request())

    def test_private_data_directory_rejects_symlinks_and_weak_permissions(self):
        for directory in (self.app.store.root, self.app.store.originals, self.app.store.previews,
                          self.app.store.staging, self.app.store.trash, self.app.store.trash_originals):
            self.assertEqual(directory.stat().st_mode & 0o777, 0o700)
        self.assertEqual(self.app.store.database.stat().st_mode & 0o777, 0o600)
        unsafe = Path(self.tmp.name) / "unsafe"
        unsafe.mkdir(mode=0o777)
        unsafe.chmod(0o777)
        with self.assertRaises(ValueError):
            CatalogStore(replace(self.config, root=unsafe))
        link = Path(self.tmp.name) / "linked"
        link.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(ValueError):
            CatalogStore(replace(self.config, root=link))

    def test_request_extra_body_is_not_reused_as_second_request(self):
        data = image_bytes()
        with self.running() as server:
            raw = server.raw(upload_raw_headers(length=len(data)) + data
                             + b"GET /icon-library/api/health HTTP/1.1\r\nHost: x\r\n\r\n")
            self.assertEqual(raw.count(b"HTTP/1.1 "), 1)
            self.assertIn(b" 200 ", raw.split(b"\r\n", 1)[0])
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)


class StubBackend:
    """Isolated HTTP /api/me fixture for exercising production verifier code."""
    def __init__(self, body=None, status=200, delay=0.0, drip=False):
        self.body = body if body is not None else {"code": 200, "data": {"id": 7, "role": 2, "disabled": False}}
        self.status, self.delay, self.drip = status, delay, drip
        self.requests = []
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                fixture.requests.append((self.path, self.headers.get("Authorization")))
                if fixture.delay:
                    time.sleep(fixture.delay)
                data = fixture.body if isinstance(fixture.body, bytes) else json.dumps(fixture.body).encode()
                try:
                    self.send_response(fixture.status)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(data)))
                    self.end_headers()
                    if fixture.drip:
                        for byte in data:
                            self.wfile.write(bytes([byte]))
                            self.wfile.flush()
                            time.sleep(0.025)
                    else:
                        self.wfile.write(data)
                except OSError:
                    pass

            def log_message(self, *args):
                pass

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = False
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": 0.02})
        self.url = "http://127.0.0.1:" + str(self.server.server_port)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
        if self.thread.is_alive():
            raise AssertionError("backend fixture did not stop")


class RealVerifierCodeTests(FixtureTest):
    def test_real_verifier_http_contract_and_double_check_on_upload(self):
        with StubBackend() as backend:
            app = IconService(replace(self.config, backend=backend.url))
            self.assertIsInstance(app.verifier, OpenListVerifier)
            with self.running(app) as server:
                self.response_json(server.upload())
            self.assertEqual(backend.requests, [("/api/me", TEST_ADMIN), ("/api/me", TEST_ADMIN)])

    def test_strict_backend_roles_disabled_code_identifier_and_no_redirects(self):
        cases = [
            ({"code": 200, "data": {"id": 7, "role": 1, "disabled": False}}, 200, 403),
            ({"code": 200, "data": {"id": 7, "role": 2, "disabled": True}}, 200, 403),
            ({"code": 200, "data": {"id": 7, "role": "2", "disabled": False}}, 200, 403),
            ({"code": 200, "data": {"id": 7, "role": 2}}, 200, 403),
            ({"code": 200, "data": {"id": 7, "role": 2, "disabled": 0}}, 200, 403),
            ({"code": 200, "data": {"id": True, "role": 2, "disabled": False}}, 200, 502),
            ({"code": 200, "data": {"id": 0, "role": 2, "disabled": False}}, 200, 502),
            ({"code": "200", "data": {}}, 200, 502),
            ({"code": 401}, 200, 401), ({"code": 403}, 200, 403), ({"code": 500}, 200, 502),
            (b"malformed backend SECRET-token", 200, 502),
            (b"x" * 65537, 200, 502), ({}, 302, 502), ({}, 500, 502), ({}, 401, 401), ({}, 403, 403),
        ]
        for body, status, expected in cases:
            with self.subTest(body=str(body)[:80], status=status), StubBackend(body=body, status=status) as backend:
                verifier = OpenListVerifier(replace(self.config, backend=backend.url))
                with self.assertRaises(Fault) as error:
                    verifier(TEST_ADMIN)
                self.assertEqual(error.exception.status, expected)
                self.assertNotIn("SECRET", str(error.exception))

    def test_backend_connection_failure_and_total_deadline(self):
        temporary_socket = socket.socket()
        temporary_socket.bind(("127.0.0.1", 0))
        port = temporary_socket.getsockname()[1]
        temporary_socket.close()
        verifier = OpenListVerifier(replace(self.config, backend=f"http://127.0.0.1:{port}", auth_timeout=0.15))
        with self.assertRaises(Fault) as error:
            verifier(TEST_ADMIN)
        self.assertEqual(error.exception.status, 502)
        for delayed, drip in ((0.35, False), (0.0, True)):
            with self.subTest(delay=delayed, drip=drip), StubBackend(delay=delayed, drip=drip) as backend:
                verifier = OpenListVerifier(replace(self.config, backend=backend.url, auth_timeout=0.15))
                started = time.monotonic()
                with self.assertRaises(Fault) as error:
                    verifier(TEST_ADMIN)
                self.assertEqual(error.exception.status, 504)
                self.assertLess(time.monotonic() - started, 0.5)

    def test_missing_authorization_never_calls_backend(self):
        with StubBackend() as backend:
            verifier = OpenListVerifier(replace(self.config, backend=backend.url))
            for token in ("", " ", " leading", "trailing ", "x\r\ny", "x" * 4097):
                with self.subTest(token=token[:30]), self.assertRaises(Fault) as error:
                    verifier(token)
                self.assertEqual(error.exception.status, 401)
            self.assertEqual(backend.requests, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
