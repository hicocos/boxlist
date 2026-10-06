#!/usr/bin/env python3
"""Build and verify an isolated Docker image; never bind host service ports."""
from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
IMAGE = "openlist-icons-repo-test:local"

# Executed only inside the isolated --network none test container.
FIXTURE = r'''
import http.client, io, json, sys, threading, urllib.parse, uuid
sys.path.insert(0, '/app')
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from PIL import Image
from icon_service.core import Config

origin = 'https://openlist.example'
token = 'isolated-docker-test-admin'
state = {'head': '', 'calls': []}
class Backend(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self):
        state['calls'].append(self.path)
        authorized = self.headers.get('Authorization') == token
        if self.path == '/api/me':
            payload = {'code':200, 'data':{'id':1, 'role':2 if authorized else 1, 'disabled':False}}
        elif self.path == '/api/admin/setting/get?key=customize_head' and authorized:
            payload = {'code':200, 'data':{'key':'customize_head', 'value':state['head']}}
        else:
            payload = {'code':403, 'data':{}}
        data = json.dumps(payload).encode()
        self.send_response(200); self.send_header('Content-Length', str(len(data)))
        self.end_headers(); self.wfile.write(data)
backend = ThreadingHTTPServer(('127.0.0.1',5244), Backend)
thread = threading.Thread(target=backend.serve_forever, daemon=True); thread.start()

def req(method, path, body=None, headers=None):
    connection = http.client.HTTPConnection('127.0.0.1',5235,timeout=10)
    try:
        connection.request(method,path,body,headers or {})
        response=connection.getresponse(); raw=response.read()
        return response.status, dict(response.getheaders()), raw
    finally: connection.close()
def data(result, status=200):
    assert result[0] == status, (result[0],result[2])
    assert result[1]['Content-Type'].startswith('application/json')
    return json.loads(result[2])
checks=[]
try:
    assert data(req('GET','/icon-library/api/health')) == {'code':200,'data':{'version':1}}
    assert data(req('GET','/icon-library/api/catalog'))['data']['assets'] == []
    checks.extend(['health 200','empty catalog 200'])
    for path in ('/icon-library/missing','/icon-library/originals/test','/icon-library/catalog.db'):
        data(req('GET',path),404)
    checks.append('unknown/private paths JSON 404 (no HTML)')
    png=io.BytesIO(); Image.new('RGBA',(40,20),(120,20,240,128)).save(png,format='PNG')
    key=uuid.uuid4().hex
    headers={'Origin':origin,'Authorization':token,'Content-Type':'application/octet-stream',
             'X-Icon-Name':urllib.parse.quote('测试.png'),'X-Upload-Id':key}
    no_auth=dict(headers); del no_auth['Authorization']
    data(req('POST','/icon-library/api/upload',png.getvalue(),no_auth),401)
    data(req('POST','/icon-library/api/upload',png.getvalue(),dict(headers,Origin='https://wrong.example')),403)
    data(req('POST','/icon-library/api/upload',png.getvalue(),dict(headers,Authorization='isolated-guest')),403)
    checks.append('missing auth 401 / foreign Origin 403 / guest role 403')
    uploaded=data(req('POST','/icon-library/api/upload',png.getvalue(),headers))['data']
    asset=uploaded['asset']; assert not uploaded['reused']
    catalog=data(req('GET','/icon-library/api/catalog'))['data']; assert catalog['assets']==[asset]
    retry=data(req('POST','/icon-library/api/upload',png.getvalue(),headers))['data']
    assert retry['reused'] and retry['asset']==asset
    checks.append('real PNG upload 200 / catalog read-back / idempotent retry')
    path='/icon-library/assets/'+asset['id']+'.webp'
    preview=req('GET',path); assert preview[0]==200 and preview[1]['Content-Type']=='image/webp'
    assert preview[1]['Cache-Control']=='public, max-age=31536000, immutable'
    with Image.open(io.BytesIO(preview[2])) as image:
        image.load(); assert image.size==(256,256) and image.format=='WEBP'
    checks.append('WebP actual decode 256x256 / immutable cache')
    theme=urllib.parse.quote(json.dumps({'version':1,'selections':{'folder':asset['id']}}))
    state['head']='<meta id="openlist-icon-config" content="'+theme+'">'
    delete_headers={'Origin':origin,'Authorization':token}
    data(req('DELETE','/icon-library/api/assets/'+asset['id'],headers=delete_headers),409)
    state['head']=''
    data(req('DELETE','/icon-library/api/assets/'+asset['id'],headers=delete_headers))
    assert data(req('GET','/icon-library/api/catalog'))['data']['assets']==[]
    data(req('GET',path),404)
    data(req('POST','/icon-library/api/upload',png.getvalue(),headers),410)
    checks.append('in-use delete 409 / delete 200 / catalog read-back / preview 404 / key tombstone 410')
    import os, stat
    root=Config.from_env().root
    assert os.getuid()==999 and os.getgid()==989
    assert root.stat().st_mode & 0o777 == 0o700
    assert (root/'trash/originals'/(asset['id']+'.original')).read_bytes()==png.getvalue()
    assert (root/'catalog.db').stat().st_mode & 0o777 == 0o600
    assert '/api/me' in state['calls'] and '/api/admin/setting/get?key=customize_head' in state['calls']
    checks.append('UID 999:GID 989 / private 0700 data / 0600 DB / retained original trash')
    print(json.dumps({'checks':checks,'asset_id':asset['id'],'fixture_backend':True},ensure_ascii=False))
finally:
    backend.shutdown(); backend.server_close(); thread.join(5)
'''


def main() -> None:
    scratch = Path(os.environ.get("OPENLIST_ICON_TEST_SCRATCH", str(Path(tempfile.gettempdir()) / "openlist-icon-tests"))).resolve()
    scratch.mkdir(mode=0o700, parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="docker-icons-", dir=scratch))
    context = work / "build"
    context.mkdir()
    for name in ("Dockerfile", ".dockerignore", "requirements.txt"):
        shutil.copy2(ROOT / "icon-service" / name, context / name)
    shutil.copytree(ROOT / "icon-service/icon_service", context / "icon_service", ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
    fixture = work / "fixture.py"
    fixture.write_text(FIXTURE, encoding="utf-8")
    fixture.chmod(0o644)
    data_dir = work / "data"
    data_dir.mkdir(mode=0o700)
    # Rootful Linux Docker uses these numeric host IDs; rootless mappings differ.
    os.chown(data_dir, 999, 989)
    container = "openlist-icons-smoke-" + uuid.uuid4().hex[:12]
    env = os.environ.copy()
    env.update(OPENLIST_ICONS_DATA=str(data_dir), OPENLIST_ICONS_ROOT="/var/lib/openlist-icons",
               OPENLIST_ICONS_BACKEND="http://127.0.0.1:5244", OPENLIST_ICONS_ORIGIN="https://openlist.example",
               OPENLIST_ICONS_IMAGE=IMAGE)
    def run(*args: str, timeout: int = 120) -> str:
        result = subprocess.run(args, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        with (work / "commands.log").open("a", encoding="utf-8") as log:
            log.write("$ " + " ".join(args) + "\n" + result.stdout + "\n")
        if result.returncode:
            raise RuntimeError(f"Command failed ({result.returncode}): {' '.join(args)}\n{result.stdout}")
        return result.stdout
    def wait_health() -> None:
        for _ in range(45):
            result = subprocess.run(["docker", "exec", container, "python", "-c",
                "import json,urllib.request; assert json.load(urllib.request.urlopen('http://127.0.0.1:5235/icon-library/api/health',timeout=1))['code']==200"],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5)
            if result.returncode == 0:
                return
            time.sleep(0.3)
        raise RuntimeError("isolated container health did not become ready")
    try:
        compose = json.loads(run("docker", "compose", "--env-file", str(ROOT / "deploy/icon-service/.env.example"),
                                 "-f", str(ROOT / "deploy/icon-service/compose.yaml"), "config", "--format", "json"))
        svc = compose["services"]["icons"]
        assert svc["network_mode"] == "host" and svc["read_only"] and svc["user"] == "999:989"
        assert svc["environment"]["OPENLIST_ICONS_ROOT"] == "/var/lib/openlist-icons"
        assert svc["volumes"][0]["source"] == str(data_dir)
        assert not svc.get("ports") and not svc.get("container_name")
        run("docker", "build", "--tag", IMAGE, str(context), timeout=600)
        run("docker", "run", "--detach", "--name", container, "--network", "none", "--read-only",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--user", "999:989",
            "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=16m,mode=1777", "--pids-limit", "64", "--memory", "256m",
            "--mount", f"type=bind,src={data_dir},dst=/var/lib/openlist-icons",
            "--mount", f"type=bind,src={fixture},dst=/fixture.py,readonly",
            "--env", "OPENLIST_ICONS_BACKEND=http://127.0.0.1:5244",
            "--env", "OPENLIST_ICONS_ORIGIN=https://openlist.example", IMAGE)
        wait_health()
        report = json.loads(run("docker", "exec", container, "python", "/fixture.py"))
        run("docker", "restart", container)
        wait_health()
        catalog = json.loads(run("docker", "exec", container, "python", "-c",
            "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:5235/icon-library/api/catalog').read().decode())"))
        assert catalog["data"]["assets"] == []
        inspect = json.loads(run("docker", "inspect", container))[0]
        assert inspect["HostConfig"]["ReadonlyRootfs"] and inspect["HostConfig"]["NetworkMode"] == "none"
        assert inspect["Config"]["User"] == "999:989"
        report["checks"].extend(["compose config valid (Linux host-network example)", "CLI restart retains deletion tombstone", "isolated runtime hardening inspected"])
        report.update(image=IMAGE, image_id=inspect["Image"], scratch=str(work), host_port_bound=False,
                      production_touched=False, limitations=["isolated backend fixture, not real administrator acceptance", "host-network compose parsed but not started (host 5235 may be occupied)"])
        (work / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(report, ensure_ascii=False, indent=2))
    finally:
        subprocess.run(["docker", "rm", "--force", container], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        result = subprocess.run(["docker", "ps", "--all", "--filter", "name=^/" + container + "$", "--format", "{{.Names}}"],
                                text=True, stdout=subprocess.PIPE, check=True, timeout=30)
        assert not result.stdout.strip(), "temporary test container was not removed"
        print("Temporary test container removed; logs: " + str(work / "commands.log"))


if __name__ == "__main__":
    main()
