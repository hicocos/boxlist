"""Production entrypoint: fixed loopback bind and always-real authorization."""
from __future__ import annotations

import argparse
import os
import signal
import sys
import threading

from .core import Config, IconService
from .httpd import IconHTTPServer


def main() -> int:
    parser = argparse.ArgumentParser(description="OpenList PNG icon upload service (127.0.0.1:5235)")
    parser.parse_args()
    os.umask(0o077)
    try:
        app = IconService(Config.from_env())
        server = IconHTTPServer(("127.0.0.1", 5235), app)
    except Exception:
        print("图标服务启动失败，请检查私有目录、配置与端口", file=sys.stderr)
        return 1
    stopping = threading.Event()

    def stop(_signum, _frame):
        if not stopping.is_set():
            stopping.set()
            threading.Thread(target=server.shutdown, daemon=True, name="icon-shutdown").start()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    print("图标服务监听 127.0.0.1:5235", flush=True)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
