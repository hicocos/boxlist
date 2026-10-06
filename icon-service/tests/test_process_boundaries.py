"""Filesystem crash windows and non-root production verifier behavior."""
from __future__ import annotations

import json
import os
import pwd
import subprocess
import sys
import unittest
import uuid
from pathlib import Path


from test_icon_service import FixtureTest, IsolatedMockVerifier, TEST_ADMIN, image_bytes
from icon_service.core import Config, IconService, convert_image

PROJECT = Path(__file__).resolve().parents[1]


class ProcessBoundaryTests(FixtureTest):
    def child(self, source, *args):
        return subprocess.run([sys.executable, "-c", source, *map(str, args)], cwd=PROJECT,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=15)

    def test_killed_before_upload_commit_does_not_publish_and_retry_recovers(self):
        key = uuid.uuid4().hex
        original = Path(self.tmp.name) / "original.png"
        preview = Path(self.tmp.name) / "preview.webp"
        data = image_bytes()
        original.write_bytes(data)
        preview.write_bytes(convert_image(data))
        code = '''import os,sys
from pathlib import Path
from icon_service.core import CatalogStore,Config
s=CatalogStore(Config(root=Path(sys.argv[1])))
class CrashConnection:
    def __init__(self, real): self.real=real
    def __getattr__(self, name): return getattr(self.real,name)
    def commit(self): os._exit(93)
real_connect=s._connect
from contextlib import contextmanager
@contextmanager
def crash_connect():
    with real_connect() as connection: yield CrashConnection(connection)
s._connect=crash_connect
s.commit('7',sys.argv[2],'crash.png',Path(sys.argv[3]).read_bytes(),Path(sys.argv[4]).read_bytes())
'''
        child = self.child(code, self.root, key, original, preview)
        self.assertEqual(child.returncode, 93, child.stderr)
        # Files really installed before death, but the row rolled back.
        self.assertEqual(len(list(self.app.store.originals.iterdir())), 1)
        self.assertEqual(self.app.store.catalog()["assets"], [])
        restarted = IconService(self.config, verifier=IsolatedMockVerifier())
        self.assertEqual(list(restarted.store.originals.iterdir()), [])
        self.assertEqual(list(restarted.store.previews.iterdir()), [])
        self.assertEqual(list(restarted.store.staging.iterdir()), [])
        with self.running(restarted) as server:
            result = self.response_json(server.upload(data, key))["data"]
            self.assertFalse(result["reused"])
            self.assertEqual(len(self.response_json(server.request())["data"]["assets"]), 1)

    def test_killed_after_delete_tombstone_before_moves_is_private_and_restart_recovers(self):
        with self.running() as server:
            asset = self.response_json(server.upload())["data"]["asset"]
        code = '''import os,sys
from pathlib import Path
from icon_service.core import CatalogStore,Config
s=CatalogStore(Config(root=Path(sys.argv[1])))
s._retire_files=lambda row: os._exit(94)
s.delete(sys.argv[2])
'''
        child = self.child(code, self.root, asset["id"])
        self.assertEqual(child.returncode, 94, child.stderr)
        self.assertEqual(len(list(self.app.store.originals.iterdir())), 1)
        self.assertEqual(self.app.store.catalog()["assets"], [])
        restarted = IconService(self.config, verifier=IsolatedMockVerifier(), head_reader=lambda token: "")
        self.assertEqual(list(restarted.store.originals.iterdir()), [])
        self.assertEqual(list(restarted.store.previews.iterdir()), [])
        self.assertEqual(len(list(restarted.store.trash_originals.iterdir())), 1)
        self.assertEqual(len(list(restarted.store.trash_previews.iterdir())), 1)
        with self.running(restarted) as server:
            self.response_json(server.delete(asset["id"]))
            self.response_json(server.request(path="/icon-library/assets/" + asset["id"] + ".webp"), 404)

    def test_low_privilege_process_can_create_store_without_service_deployment(self):
        if os.geteuid() != 0:
            self.skipTest("requires root only to launch isolated nobody child")
        user = pwd.getpwnam("nobody")
        scratch = Path(self.tmp.name) / "non-root"
        scratch.mkdir(mode=0o700)
        os.chown(scratch, user.pw_uid, user.pw_gid)
        code = '''import os,sys,json
from pathlib import Path
from icon_service.core import CatalogStore,Config
# Chroot only this short-lived child to the isolated scratch fixture, then
# drop privileges before real mkdir/SQLite/fsync. Never relax /root permissions.
os.chroot(sys.argv[1])
os.chdir('/')
os.setgroups([])
os.setgid(int(sys.argv[2]))
os.setuid(int(sys.argv[3]))
s=CatalogStore(Config(root=Path('/data')))
print(json.dumps({'uid':os.geteuid(),'mode':s.root.stat().st_mode&0o777,'catalog':s.catalog()}))
'''
        child = self.child(code, scratch, user.pw_gid, user.pw_uid)
        self.assertEqual(child.returncode, 0, child.stderr)
        result = json.loads(child.stdout)
        self.assertEqual(result, {"uid": user.pw_uid, "mode": 0o700,
                                 "catalog": {"version": 1, "assets": [], "hidden_builtins": []}})


class RealBackendReadOnlyProbe(unittest.TestCase):
    @unittest.skipUnless(os.environ.get("OPENLIST_ICON_TEST_LIVE_READ_ONLY") == "1", "live read-only probe is explicit opt-in")
    def test_real_backend_guest_no_admin_acceptance(self):
        from icon_service.core import Fault, OpenListVerifier
        # Synthetic invalid marker only; never extract or invent an admin token.
        marker = "icon-service-read-only-invalid-token-" + uuid.uuid4().hex
        with self.assertRaises(Fault) as error:
            OpenListVerifier(Config.from_env())(marker)
        self.assertIn(error.exception.status, (401, 403))
        self.assertEqual(str(error.exception), "登录无效或权限不足")


if __name__ == "__main__":
    unittest.main(verbosity=2)
