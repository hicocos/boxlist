# 独立 PNG 图标服务

本目录是自定义前端的可选 API，不是 OpenList 后端替代品。完整中文接入、Docker 启动、nginx、接口和迁移边界见 [图标服务接入文档](../docs/icon-service.md)。

- `icon_service/core.py`：真实后端鉴权、PNG 转换、SQLite/WAL、删除保护和私有回收目录。
- `icon_service/httpd.py`：严格路由、限流和请求期限；不存在的资源不会返回 HTML。
- `icon_service/__main__.py`：`python -m icon_service`，固定监听 `127.0.0.1:5235`。
- `Dockerfile`：Python 3.13 slim / Pillow 12.3.0，非 root `999:989`。
- `tests/`：真实 PNG/WebP、磁盘、HTTP socket、崩溃恢复与鉴权 fixture 测试。

默认配置均为示例：`OPENLIST_ICONS_ROOT=/var/lib/openlist-icons`、`OPENLIST_ICONS_BACKEND=http://127.0.0.1:5244`、`OPENLIST_ICONS_ORIGIN=https://openlist.example`。部署前必须替换后端端口和 HTTPS Origin，并为运行 UID 准备私有数据目录。没有跳过鉴权的环境变量。

## 本地测试（默认不访问真实后端）

在本目录运行，使用 Python 3.13：

```sh
python -m venv .venv
.venv/bin/python -m pip install -r requirements.txt
OPENLIST_ICON_TEST_SCRATCH="$PWD/.test-scratch" .venv/bin/python -W error::ResourceWarning -m unittest discover -s tests -v
```

`OPENLIST_ICON_TEST_SCRATCH` 可选；默认是 `tempfile.gettempdir()/openlist-icon-tests`。每个 fixture 使用独立临时子目录并清理。不要指向真实数据目录；测试 mock 仅用于隔离测试，不构成真实管理员上传验收。低权限 chroot 测试在非 root 环境自动跳过。

Docker 打包 smoke test（在仓库根目录，要求 Docker daemon 与 Linux）：

```sh
OPENLIST_ICON_TEST_SCRATCH="$PWD/.docker-test-scratch" python3 deploy/icon-service/smoke-test.py
```

测试复制最小构建上下文到 scratch，构建真实镜像并在独立无外网容器中运行 CLI、HTTP、PNG 上传/删除和 fixture 鉴权；不抢占宿主机 5235，不接触真实数据库，结束移除容器，保留 scratch 报告和本地测试镜像。

## 可选真实后端只读拒绝探针

**默认跳过。** 仅在明确允许访问目标后端后手动运行：

```sh
OPENLIST_ICON_TEST_LIVE_READ_ONLY=1 \
OPENLIST_ICONS_BACKEND=http://127.0.0.1:5244 \
OPENLIST_ICONS_ORIGIN=https://openlist.example \
OPENLIST_ICON_TEST_SCRATCH="$PWD/.test-scratch" \
.venv/bin/python -m unittest discover -s tests -p test_process_boundaries.py -v
```

只向指定后端 `/api/me` 发送随机无效测试标记，检查 401/403；不读取凭证、不修改设置、不上传、不删除。后端不可达会使该探针失败，而非证明权限校验成功。切勿把 fixture 管理员或该探针当作生产管理员验收。
