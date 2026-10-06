"""Authenticated deletion, fail-closed usage checks, and recoverable trash."""
from __future__ import annotations

import contextlib
import json
import sqlite3
import threading
import time
import unittest
import uuid
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from urllib.parse import quote
from unittest.mock import patch

from icon_service.core import BUILTIN_IDS, CatalogStore, Config, Fault, IconService, OpenListVerifier, head_references
from test_icon_service import (
    FixtureTest, IsolatedMockVerifier, ORIGIN, StubBackend, TEST_ADMIN, image_bytes,
)


def theme_head(selections=None):
    value = quote(json.dumps({"version": 1, "selections": selections or {}}, ensure_ascii=False, separators=(",", ":")), safe="~()*!.'-")
    return ('<!-- OPENLIST-ICON-THEME-START -->\n<meta id="openlist-icon-config" content="' + value
            + '">\n<!-- OPENLIST-ICON-THEME-END -->')


class IsolatedHeadReader:
    def __init__(self, head=""):
        self.head = head
        self.calls = []
        self.error = None

    def __call__(self, token):
        self.calls.append(token)
        if self.error:
            raise self.error
        return self.head


class DeleteTests(FixtureTest):
    def setUp(self):
        super().setUp()
        self.head = IsolatedHeadReader()
        self.app = IconService(self.config, verifier=self.verifier, head_reader=self.head)

    def test_delete_permission_origin_disabled_empty_and_api_failure_denied(self):
        with self.running() as server:
            for token, expected in (("", 401), ("isolated-test-guest", 403), ("isolated-test-disabled", 403),
                                    ("isolated-test-api-failure", 502), ("isolated-test-api-timeout", 504)):
                with self.subTest(token=token):
                    self.response_json(server.delete("pack-folder", token), expected)
            self.response_json(server.delete("pack-folder", omit=("Authorization",)), 401)
            self.response_json(server.delete("pack-folder", omit=("Origin",)), 403)
            for origin in ("null", "https://openlist.example/", "http://openlist.example", "https://foreign.example"):
                self.response_json(server.delete("pack-folder", overrides={"Origin": origin}), 403)
            self.assertEqual(self.head.calls, [])
            self.assertEqual(self.response_json(server.request())["data"]["hidden_builtins"], [])

    def test_uploaded_in_use_any_target_and_builtin_are_authoritatively_blocked(self):
        with self.running() as server:
            asset = self.response_json(server.upload())["data"]["asset"]
            for asset_id in (asset["id"], "pack-folder", "legacy-smile"):
                self.head.head = theme_head({"some-unknown-target": asset_id})
                value = self.response_json(server.delete(asset_id), 409)
                self.assertEqual(value["message"], "正在使用，请先更换并保存")
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)
            self.assertEqual(self.response_json(server.request())["data"]["hidden_builtins"], [])
            self.assertEqual(len(self.head.calls), 3)

    def test_head_network_failure_invalid_encoding_and_ambiguous_meta_fail_closed(self):
        with self.running() as server:
            for status in (502, 504, 403):
                self.head.error = Fault(status, "无法核对图标配置，未删除")
                self.response_json(server.delete("pack-folder"), status)
            self.head.error = None
            malformed = [
                '<meta id="openlist-icon-config" content="%ZZ">',
                '<meta id="openlist-icon-config" content="%ff">',
                '<meta id="openlist-icon-config" content="{}">',
                '<meta id="openlist-icon-config">',
                '<meta id="openlist-icon-config" content="" content="other">',
                '<!-- OPENLIST-ICON-THEME-START -->missing meta<!-- OPENLIST-ICON-THEME-END -->',
                theme_head() + theme_head(),
                '<!-- OPENLIST-FOLDER-ICON-START -->invalid<!-- OPENLIST-FOLDER-ICON-END -->',
                '<script>window.OPENLIST_FOLDER_ICON_STYLE="smile";</script>',
            ]
            for head in malformed:
                with self.subTest(head=head[:100]):
                    self.head.head = head
                    self.response_json(server.delete("pack-folder"), 502)
            self.assertEqual(self.response_json(server.request())["data"]["hidden_builtins"], [])

    def test_legacy_smile_blocked_without_executing_script(self):
        self.head.head = ('<!-- OPENLIST-FOLDER-ICON-START -->\n'
                          '<script>window.OPENLIST_FOLDER_ICON_STYLE="smile";dangerous()</script>\n'
                          '<!-- OPENLIST-FOLDER-ICON-END -->')
        with self.running() as server:
            self.response_json(server.delete("legacy-smile"), 409)
            self.response_json(server.delete("pack-folder"))
            self.head.head = theme_head() + self.head.head
            self.response_json(server.delete("legacy-smile"), 409)

    def test_uploaded_deleted_catalog_404_trash_idempotency_and_cold_restart(self):
        key, data = uuid.uuid4().hex, image_bytes()
        with self.running() as server:
            asset = self.response_json(server.upload(data, key))["data"]["asset"]
            removed = self.response_json(server.delete(asset["id"]))["data"]
            self.assertEqual(removed, {"id": asset["id"], "kind": "uploaded", "removed": True})
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.response_json(server.request(path="/icon-library/assets/" + asset["id"] + ".webp"), 404)
            self.response_json(server.request(path="/icon-library/trash/originals/" + asset["id"] + ".original"), 404)
            self.assertEqual(self.response_json(server.delete(asset["id"]))["data"], removed)
            self.response_json(server.upload(data, key), 410)
        self.assertEqual(list(self.app.store.originals.iterdir()), [])
        self.assertEqual(list(self.app.store.previews.iterdir()), [])
        self.assertEqual((self.app.store.trash_originals / (asset["id"] + ".original")).read_bytes(), data)
        self.assertEqual((self.app.store.trash_previews / (asset["id"] + ".webp")).stat().st_size, asset["bytes"])
        with contextlib.closing(sqlite3.connect(self.app.store.database)) as db:
            row = db.execute("SELECT deleted_at,request_id FROM assets WHERE id=?", (asset["id"],)).fetchone()
            self.assertTrue(row[0])
            self.assertEqual(row[1], key)
        restarted = IconService(self.config, verifier=self.verifier, head_reader=self.head)
        with self.running(restarted) as server:
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.response_json(server.request(path="/icon-library/assets/" + asset["id"] + ".webp"), 404)
            self.assertEqual(self.response_json(server.delete(asset["id"]))["data"], removed)
            self.response_json(server.upload(data, key), 410)
            new = self.response_json(server.upload(data, uuid.uuid4().hex))["data"]["asset"]
            self.assertNotEqual(new["id"], asset["id"])

    def test_exact_21_builtins_tombstones_persist_and_never_touch_source_files(self):
        expected = {
            "pack-cab", "pack-doc", "pack-docx", "pack-mp3", "pack-pdf", "pack-ppt", "pack-pptx",
            "pack-psd", "pack-rar", "pack-txt", "pack-wav", "pack-wma", "pack-xlsx", "pack-zip",
            "pack-download", "pack-image", "pack-folder", "pack-document", "pack-video", "pack-music", "legacy-smile",
        }
        self.assertEqual(BUILTIN_IDS, expected)
        self.assertEqual(len(BUILTIN_IDS), 21)
        with self.running() as server:
            for asset_id in sorted(expected):
                result = self.response_json(server.delete(asset_id))["data"]
                self.assertEqual(result, {"id": asset_id, "kind": "builtin", "removed": True})
            self.response_json(server.delete("pack-folder"))
            catalog = self.response_json(server.request())["data"]
            self.assertEqual(catalog["hidden_builtins"], sorted(expected))
            self.assertEqual(catalog["assets"], [])
        restarted = IconService(self.config, verifier=self.verifier, head_reader=self.head)
        self.assertEqual(restarted.store.catalog(), {"version": 1, "assets": [], "hidden_builtins": sorted(expected)})
        # Deletion of builtin assets only updates SQLite, creates no fake preview
        # or original and has no configured path to frontend/dist source assets.
        for directory in (restarted.store.originals, restarted.store.previews,
                          restarted.store.trash_originals, restarted.store.trash_previews):
            self.assertEqual(list(directory.iterdir()), [])

    def test_delete_invalid_id_unknown_upload_and_nonempty_body_rejected(self):
        with self.running() as server:
            for asset_id in ("pack-not-real", "legacy-SmILE", "../catalog.db", "upload-" + "F" * 32,
                             "upload-" + "0" * 31, "upload-" + "0" * 32):
                self.response_json(server.delete(asset_id), 404)
            self.response_json(server.delete("pack-folder", overrides={"Content-Length": "1"}), 400)
            # A syntactically valid but unknown upload is still fully checked
            # before the database determines it does not exist.
            self.assertEqual(self.head.calls, [TEST_ADMIN, TEST_ADMIN])

    def test_second_head_read_catches_intervening_use_and_releases_pending(self):
        heads = iter([theme_head(), theme_head({"folder": "pack-folder"})])
        self.app = IconService(self.config, verifier=self.verifier, head_reader=lambda token: next(heads))
        with self.running() as server:
            self.response_json(server.delete("pack-folder"), 409)
            self.assertEqual(self.app._pending_deletes, set())
            self.assertEqual(self.response_json(server.request())["data"]["hidden_builtins"], [])
        self.app.head_reader = IsolatedHeadReader()
        with self.running() as server:
            self.response_json(server.delete("pack-folder"))

    def test_delete_revocation_checked_before_tombstone(self):
        calls = []

        def revokes(token):
            calls.append(token)
            if len(calls) > 1:
                raise Fault(403, "权限不足")
            return self.verifier(token)

        self.app = IconService(self.config, verifier=revokes, head_reader=self.head)
        with self.running() as server:
            self.response_json(server.delete("pack-folder"), 403)
            self.assertEqual(self.response_json(server.request())["data"]["hidden_builtins"], [])
            self.assertEqual(len(self.head.calls), 1)

    def test_pending_delete_serialized_with_other_delete_requests(self):
        entered, release = threading.Event(), threading.Event()

        def hold(token):
            entered.set()
            if not release.wait(5):
                raise AssertionError("release missing")
            return ""

        self.app.head_reader = hold
        with self.running() as server, ThreadPoolExecutor(max_workers=1) as pool:
            pending = pool.submit(server.delete, "pack-folder")
            try:
                self.assertTrue(entered.wait(3))
                self.assertEqual(self.app._pending_deletes, {"pack-folder"})
                self.response_json(server.delete("pack-image"), 429)
            finally:
                release.set()
            self.response_json(pending.result())
            self.response_json(server.delete("pack-image"))
            self.assertEqual(self.app._pending_deletes, set())

    def test_retirement_failure_is_nonpublic_and_cold_restart_recovers(self):
        with self.running() as server:
            asset = self.response_json(server.upload())["data"]["asset"]
            with patch.object(self.app.store, "_retire_files", side_effect=OSError("SECRET-do-not-log")):
                self.response_json(server.delete(asset["id"]), 503)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])
            self.response_json(server.request(path="/icon-library/assets/" + asset["id"] + ".webp"), 404)
        restarted = IconService(self.config, verifier=self.verifier, head_reader=self.head)
        self.assertEqual(list(restarted.store.originals.iterdir()), [])
        self.assertEqual(list(restarted.store.previews.iterdir()), [])
        self.assertEqual(len(list(restarted.store.trash_originals.iterdir())), 1)
        self.assertEqual(len(list(restarted.store.trash_previews.iterdir())), 1)
        with self.running(restarted) as server:
            self.response_json(server.delete(asset["id"]))

    def test_recoverable_trash_still_consumes_original_storage_quota(self):
        data = image_bytes()
        self.app = IconService(replace(self.config, max_original_bytes=len(data)), verifier=self.verifier, head_reader=self.head)
        with self.running() as server:
            asset = self.response_json(server.upload(data))["data"]["asset"]
            self.response_json(server.delete(asset["id"]))
            self.response_json(server.upload(data, uuid.uuid4().hex), 507)
            self.assertEqual(self.response_json(server.request())["data"]["assets"], [])

    def test_recoverable_trash_counts_toward_200_item_budget(self):
        self.app = IconService(replace(self.config, max_assets=1), verifier=self.verifier, head_reader=self.head)
        with self.running() as server:
            asset = self.response_json(server.upload())["data"]["asset"]
            self.response_json(server.delete(asset["id"]))
            self.response_json(server.upload(request_id=uuid.uuid4().hex), 507)
            self.assertEqual(len(list(self.app.store.trash_originals.iterdir())), 1)

    def test_idempotent_upload_retry_denied_while_asset_deletion_pending(self):
        key, data = uuid.uuid4().hex, image_bytes()
        entered, release = threading.Event(), threading.Event()

        def hold(token):
            entered.set()
            if not release.wait(5):
                raise AssertionError("release missing")
            return ""

        with self.running() as server, ThreadPoolExecutor(max_workers=1) as pool:
            asset = self.response_json(server.upload(data, key))["data"]["asset"]
            self.app.head_reader = hold
            pending = pool.submit(server.delete, asset["id"])
            try:
                self.assertTrue(entered.wait(3))
                self.response_json(server.upload(data, key), 409)
            finally:
                release.set()
            self.response_json(pending.result())
            self.response_json(server.upload(data, key), 410)


class HeadParserTests(unittest.TestCase):
    def test_percent_encoded_theme_and_safe_html_attribute_parsing(self):
        uploaded = "upload-" + "1" * 32
        self.assertEqual(head_references(theme_head({"folder": "pack-folder", "x": uploaded, "ext:txt": "default"})),
                         {"pack-folder", uploaded, "default"})
        content = quote(json.dumps({"version": 1, "selections": {"folder": "legacy-smile"}}))
        self.assertEqual(head_references("<META content='" + content + "' id='openlist-icon-config'/ >"), {"legacy-smile"})
        self.assertEqual(head_references("<style>.safe{color:red}</style>"), set())
        self.assertEqual(head_references(""), set())

    def test_bad_schema_duplicate_json_keys_unknown_values_and_excess_size_fail_closed(self):
        contents = [
            '{"version":1,"selections":{},"selections":{"folder":"pack-folder"}}',
            '{"version":2,"selections":{}}', '{"version":1,"selections":[]}',
            '{"version":1,"selections":{"folder":false}}',
            '{"version":1,"selections":{"folder":"pack-not-real"}}',
        ]
        for content in contents:
            with self.subTest(content=content), self.assertRaises(Fault):
                head_references('<meta id="openlist-icon-config" content="' + quote(content) + '">')
        with self.assertRaises(Fault):
            head_references("x" * (1024 * 1024 + 1))


class ProductionHeadClientTests(FixtureTest):
    def test_production_delete_reads_real_http_api_twice_without_backend_writes(self):
        class MixedBackend(StubBackend):
            pass

        with MixedBackend() as backend:
            # Fixture changes reply according to path; all calls are actual HTTP.
            original_handler = backend.server.RequestHandlerClass

            class Handler(original_handler):
                def do_GET(self):
                    if self.path == "/api/admin/setting/get?key=customize_head":
                        backend.requests.append((self.path, self.headers.get("Authorization")))
                        body = json.dumps({"code": 200, "data": {"key": "customize_head", "value": theme_head()}}).encode()
                        self.send_response(200)
                        self.send_header("Content-Length", str(len(body)))
                        self.end_headers()
                        self.wfile.write(body)
                    else:
                        super().do_GET()

            backend.server.RequestHandlerClass = Handler
            self.app = IconService(replace(self.config, backend=backend.url))
            with self.running() as server:
                self.response_json(server.delete("pack-folder"))
            self.assertEqual(backend.requests, [
                ("/api/me", TEST_ADMIN), ("/api/admin/setting/get?key=customize_head", TEST_ADMIN),
                ("/api/me", TEST_ADMIN), ("/api/admin/setting/get?key=customize_head", TEST_ADMIN),
            ])

    def test_production_head_reader_fail_closed_bad_api_response(self):
        cases = [
            ({"code": 200, "data": {"value": None}}, 200, 502),
            ({"code": 200, "data": {"value": "", "key": "other"}}, 200, 502),
            ({"code": 500, "data": {}}, 200, 502),
            (b"SECRET malformed reply", 200, 502), ({}, 500, 502), ({}, 403, 403),
        ]
        for body, status, expected in cases:
            with self.subTest(status=status, body=str(body)[:80]), StubBackend(body=body, status=status) as backend:
                client = OpenListVerifier(replace(self.config, backend=backend.url))
                with self.assertRaises(Fault) as error:
                    client.read_head(TEST_ADMIN)
                self.assertEqual(error.exception.status, expected)
                self.assertNotIn("SECRET", str(error.exception))


if __name__ == "__main__":
    unittest.main(verbosity=2)
