#!/usr/bin/env python3
"""Test nginx example with an isolated fake upstream, never the actual site."""
from __future__ import annotations

import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time


def main() -> None:
    nginx = shutil.which("nginx")
    if not nginx:
        raise SystemExit("nginx not installed; isolated nginx test not run")
    scratch = Path(os.environ.get("OPENLIST_ICON_TEST_SCRATCH", str(Path(tempfile.gettempdir()) / "openlist-icon-tests")))
    scratch.mkdir(mode=0o700, parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix="nginx-icons-", dir=scratch)).resolve()
    (work / "logs").mkdir()
    # Optional local nginx/OpenResty module paths; ordinary nginx needs none.
    lua_path = os.environ.get("NGINX_TEST_LUA_PATH", "")
    lua_config = f"lua_package_path '{lua_path}';" if lua_path else ""
    class Fixture(BaseHTTPRequestHandler):
        def log_message(self, *args): pass
        def do_GET(self):
            payload = json.dumps({"code":404,"path":self.path,"auth":self.headers.get("Authorization"),
                                  "origin":self.headers.get("Origin")}).encode()
            self.send_response(404); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload))); self.end_headers(); self.wfile.write(payload)
    backend = ThreadingHTTPServer(("127.0.0.1",0), Fixture)
    thread = threading.Thread(target=backend.serve_forever, daemon=True); thread.start()
    with socket.socket() as probe:
        probe.bind(("127.0.0.1",0)); port = probe.getsockname()[1]
    snippet = Path(__file__).with_name("nginx.conf.example").read_text().replace("127.0.0.1:5235", f"127.0.0.1:{backend.server_port}")
    config = work / "nginx.conf"
    # Parent HTML error_page deliberately tests that icon location overrides it.
    config.write_text(f'''daemon off;
master_process off;
pid {work}/nginx.pid;
error_log {work}/error.log;
events {{ worker_connections 32; }}
http {{
  {lua_config}
  access_log off;
  client_body_temp_path {work}/body;
  proxy_temp_path {work}/proxy;
  server {{
    listen 127.0.0.1:{port};
    error_page 404 413 502 = /index.html;
    location = /index.html {{ return 200 "<html>unwanted SPA fallback</html>"; }}
    {snippet}
  }}
}}
''')
    validation = subprocess.run([nginx,"-t","-p",str(work)+"/","-c",str(config)],text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,check=True)
    process = subprocess.Popen([nginx,"-p",str(work)+"/","-c",str(config)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    def get(path,headers=None):
        conn = http.client.HTTPConnection("127.0.0.1",port,timeout=3)
        try:
            conn.request("GET",path,headers=headers or {})
            response=conn.getresponse(); return response.status,dict(response.getheaders()),response.read()
        finally: conn.close()
    try:
        for _ in range(50):
            try: result=get("/icon-library/missing"); break
            except ConnectionRefusedError: time.sleep(.05)
        else:
            process.terminate()
            stdout, stderr = process.communicate(timeout=5)
            raise RuntimeError(f"isolated nginx not ready (exit={process.returncode}): {stderr.decode()} {stdout.decode()}")
        assert result[0]==404 and json.loads(result[2])["path"]=="/icon-library/missing"
        result=get("/icon-library/api/catalog",{"Authorization":"isolated-nginx-test","Origin":"https://openlist.example"})
        decoded=json.loads(result[2])
        assert result[0]==404 and decoded["auth"]=="isolated-nginx-test" and decoded["origin"]=="https://openlist.example"
        decoded=json.loads(get("/icon-library/api/catalog")[2])
        assert decoded["auth"] is None and decoded["origin"] is None
        conn = http.client.HTTPConnection("127.0.0.1",port,timeout=3)
        try:
            conn.request("POST", "/icon-library/api/upload", headers={"Content-Length":"5242881"})
            response=conn.getresponse()
            assert response.status==503 and json.loads(response.read())["code"]==503
        finally:
            conn.close()
        backend.shutdown(); backend.server_close(); thread.join(5)
        result=get("/icon-library/api/health")
        assert result[0]==503 and result[1]["Content-Type"]=="application/json" and json.loads(result[2])["code"]==503
        report={"checks":["nginx -t passed", "upstream 404 stays JSON 404 despite inherited HTML error_page",
                          "Authorization and Origin forwarded unchanged; absent headers not invented", "nginx-generated oversized body returns JSON 503 not inherited HTML", "unavailable upstream returns JSON 503 not HTML"],
                "production_touched":False,"scratch":str(work)}
        (work/"report.json").write_text(json.dumps(report,indent=2)+"\n")
        print(validation.stdout+json.dumps(report,indent=2))
    finally:
        process.terminate()
        process.communicate(timeout=5)
        if thread.is_alive():
            backend.shutdown(); backend.server_close(); thread.join(5)


if __name__ == "__main__":
    main()
