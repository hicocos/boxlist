# 包装验证记录（不等于发布/生产验收）

本次已执行：

- `sh -n scripts/install.sh`、`sh scripts/install.sh --help`：成功。
- `koalaman/shellcheck:stable --shell=sh /src/install.sh`：成功，无诊断。
- `rhysd/actionlint:latest /src/workflow.yml`：成功，无诊断；初次 glob 提示已修复并重跑。
- `docker buildx build --check .`：`Check complete, no warnings found.`，Go/Node/runtime 镜像元数据解析成功；这不是完整编译。
- 提供占位但格式有效的环境变量执行 `docker compose -f deploy/integrated/compose.yaml config --quiet`：成功，未 up/拉取业务镜像。
- 真实 Docker 只读 `install.sh ... --dry-run`：成功；未拉取目标镜像、创建目标目录或启动安装。
- 独立 scratch harness **17** 项通过：fixed tag/digest dry-run；拒绝 latest、无版本、越界/注入端口、registry-port 无 tag、错误 digest、version/image 混用、已有/符号链接/相对目录、同名容器、监听端口、Docker 已映射端口、daemon 不可用；mock pull 失败时不创建数据目录。
- `git diff --check`：成功。

Harness 使用 mock Docker/ss 明确模拟冲突/失败，不能视作真实镜像/服务结果；真实 dry-run 另行执行。验证用 lint 容器临时运行且无网络、只读挂载，不安装 OpenList，不修改现有业务容器/数据。

## 已补充实际运行验收

- 124 项前端测试通过；类型检查、完整构建通过。
- `go test -race -count=1 ./internal/iconlibrary ./server` 通过；`CGO_ENABLED=0` 完整后端编译通过。
- 根 Dockerfile 完整源码构建成功，固定测试镜像 `openlist-custom:verification`；发现并修复安装依赖前未复制 `pnpm-workspace.yaml` 导致 patchedDependencies 与锁文件不匹配的问题。
- 真正全新数据目录、随机容器名、仅 loopback 端口的原生运行验收通过：native ping/settings、嵌入前端入口资源、私有路径 JSON404、真实初始管理员登录、PNG 上传/原 key 幂等重试/WebP 解码、匿名401/错误 Origin403。
- 同一有效 JWT 在真实隔离数据库禁用/降权后403、密码版本变化后401；原生 token 轮换后旧 token401；logout 后旧 JWT401。数据库修改仅用于全新隔离实例的撤销测试，不接触生产。
- 真实设置 API 写入图标引用后删除409，解除引用后容器重启仍保留素材，再次登录后删除成功且 catalog/preview 读回确认；进程检查仅原生 Go，无 Python/nginx。
- 可复用运行脚本：`tests/integrated-image-smoke.py`（测试依赖 Pillow，不是镜像运行依赖），明确要求专用 scratch 环境变量与 root；所有测试容器结束后移除，报告/脱敏日志仅留 scratch。Docker 随机映射端口可能在 restart 后改变，测试必须重新读取 `docker port`，不能把旧端口返回内容当目标实例结果。

未执行生产安装、GHCR push、GitHub Release、公开仓库切换。公共版本仍待素材授权和历史敏感信息审查；验收不代表旧 sidecar 数据自动迁移、跨请求事务一致性或其它架构已验证。
