# 后端变更：OpenList v4.2.6 原生图标模块

## 基线与范围

上游为 [OpenListTeam/OpenList tag v4.2.6](https://github.com/OpenListTeam/OpenList/tree/v4.2.6)，commit **`2bdf16d5967d0a403f67d809efd5a418b8f5bd30`**。源码位于 `backend/`，前端仍位于项目根目录；不要把根仓库 commit 当成上游后端 commit。

### 确切路径

新增模块文件：

- `backend/internal/iconlibrary/http.go`：独立 `net/http` 服务、严格路由/请求头、限流、上传/删除。
- `backend/internal/iconlibrary/store.go`：私有 SQLite/磁盘目录、请求幂等、提交恢复、tombstone/回收数据、配额。
- `backend/internal/iconlibrary/schema.go`：打开已有目录时先验证 schema/记录和容量，不把未知目录当正常素材库处理。
- `backend/internal/iconlibrary/validation.go`：静态 PNG 校验/实际解码和纯 Go `nativewebp` 编码。
- `backend/internal/iconlibrary/head.go`：非执行托管 head 图标配置引用读取/严格解析。
- `backend/internal/iconlibrary/exif.go`：限制边界的 PNG EXIF orientation 读取，不把不可信 metadata 当文件系统路径/任意指针处理。
- `backend/internal/iconlibrary/{iconlibrary_test.go,bounds_test.go,recovery_test.go}`：模块隔离/边界/恢复测试与 fuzz；不属于上游运行时接入点。
- `backend/internal/iconlibrary/README.md`：模块契约和迁移/安全限制。
- `backend/server/icon_library_test.go`：HTTPS Origin 适配边界测试。

上游运行时接入仅限：

- **新增** `backend/server/icon_library.go`：用真实 OpenList 原生 token/JWT 和用户状态鉴权；直接读取原生数据库最新 `customize_head`；检查实际 HTTPS Origin；将目录绑定到 `<flags.DataDir>/icon`。
- **修改** `backend/server/router.go`：两行调用，在静态处理前接入新 `/icon`、`/icon/*path`，不改 `/api/` 登录或存储逻辑。
- **修改** `backend/go.mod`、`backend/go.sum`：Go 1.27.0 工具链及素材模块依赖；配套工具链/依赖更新需与上游基线分开审查，不能把更新说成原始 tag 已自带。

构建生成 `backend/public/dist/` 是给 `backend/public/public.go` 已有 `//go:embed all:dist` 提供完整二改 UI；不用修改该 embed 代码或 `backend/server/static/static.go`。根 Dockerfile 不执行上游 `build.sh`，避免它联网下载另一个上游 frontend。运行时默认 `dist_dir` 空使用嵌入前端；非空值保持原生覆盖行为，不自动修改业务配置。

## API

| 方法/路径 | 行为 |
| --- | --- |
| `GET /icon/api/health` | 模块连通性；不能替代管理员权限/持久化验收 |
| `GET /icon/api/catalog` | 公开可选目录与隐藏项状态；不含私有原图 |
| `GET /icon/assets/upload-<32hex>.webp` | 已提交且未删除的同源预览 |
| `POST /icon/api/upload` | 管理员 PNG 原始二进制 body；非 multipart |
| `DELETE /icon/api/assets/<id>` | 管理员删除上传素材或隐藏内置 ID |

上传请求使用真实 native `Authorization`、实际 HTTPS `Origin`、`Content-Type: application/octet-stream`、准确 `Content-Length`、`X-Upload-Id`（32 位小写 hex 请求标识）、`X-Icon-Name`（percent-encoded PNG 名称）。失败或不确定提交后以原请求标识核对/重试；不要生成新标识制造重复提交。模块不支持任意路径别名、查询参数、原图下载或公开数据库路径；错误响应使用固定消息，不泄露原始文件路径/数据库错误。

## 数据与限制

私有根为 `<flags.DataDir>/icon`，集成镜像中为 `/opt/openlist/data/icon`；随完整数据卷一起备份。原图、预览、数据库、`.staging` 和 `trash/` 由模块管理，不通过静态文件系统 alias 暴露。

当前代码限制：单文件 **1 byte–5 MiB**，单边最大 **4096px**、总像素不超过 **16,000,000**；生成透明 **256×256 WebP**，主体等比缩放至最多 232×232，不放大小图。最多 **200** 条上传资产（含删除保留记录）、总容量 **256 MiB**（原图+预览，含私有回收数据）。目录/配额以当前代码为准；不要把 UI 限制当服务端限制，也不要手动清空 tombstone 来绕过容量。

WebP 使用 `nativewebp` 纯 Go 编码；镜像 `CGO_ENABLED=0` 编译。Linux 私有目录/锁防护由模块负责，当前镜像验证目标是 `linux/amd64`，未宣称已验证其它 OS/架构。

## 安全边界（不要夸大）

- 上传和删除都要求当前启用管理员，复核原生 JWT/token 与当前用户状态，不接受独立 sidecar token/环境变量鉴权绕过。
- 写操作要求单一精确 HTTPS Origin。反代下配置规范 `SITE_URL`；未配置时只能匹配 `https://r.Host`，不信任任意 `X-Forwarded-Host`。
- 删除再次读取最新 `customize_head` 并检查引用；解析/读取失败拒绝删除。素材模块串行化自己的提交，但**不和其它设置 API 组成一个数据库事务**，因此外部写入/其它浏览器仍存在 read-before-delete 的跨请求竞争窗口。
- WebP 预览为 immutable 长缓存。源端删除后绕过浏览器/CDN缓存验证 404；不能保证已缓存字节即时消失。
- 内置删除只隐藏目录 ID，不从前端源码删除素材。上传删除保留 tombstone/私有回收数据，不提供自动清理保证或管理员回收站恢复 UI 承诺。
- 旧 Python schema 的兼容读取逻辑不等于生产迁移验收；旧 `/icon-library/` URL、已保存选择和旧目录必须单独核对。

## 升级上游清单

1. 记录新上游 tag/commit，取得干净源码与其许可证；先备份现有代码/数据，隔离测试，不直接替换生产。
2. 将完整 `internal/iconlibrary/` 独立模块移入新上游；先比对上游是否占用同名路由/包，不重写整份 `router.go`。
3. 单独重放 `server/icon_library.go` 与最小两行 router 调用。重新检查 `flags.DataDir`、原生 token/JWT、用户禁用/密码变更、`customize_head` 新鲜读取与同源 HTTPS 判断是否仍正确。
4. 用新上游为基准合并 `go.mod` / `go.sum`，审查工具链与传递依赖变更，确认纯 Go/SQLite 在目标架构可用。
5. 在编译前复制本项目完整 frontend `dist/` 至 `backend/public/dist/`；确认 `all:dist` embed 和 native `dist_dir` 覆盖行为不变，避免下载错误 UI。
6. 运行前端 `test:ux`、lint、build；Go 模块与路由测试；真实新镜像构建和单进程/版本/HTTP/匿名拒绝/合法管理员/重启持久化验收。测试需使用独立数据、名称、端口，不能重置生产密码来获取验收条件。
7. 对照上游 diff 确认没有修改登录/存储驱动/账号模型，更新本文基线及文件清单。未知新增依赖/素材许可证先审查再发布。
8. 更新固定版本 Release、SHA256SUMS、源码归档与镜像；从隔离副本验证备份/回滚后，另行授权生产迁移。

## 打包与许可证

`Dockerfile` 是三阶段源码构建，runtime 直接 Go 二进制为 PID 1，非 root `1001:1001`，不含 Python/nginx/aria2 进程；Node/Go 编译器仅在构建阶段。前端也安装到 `/opt/openlist/dist`，默认使用相同版本的内嵌资源。

根 `LICENSE` 是前端上游 MIT；`backend/LICENSE` 为 AGPL-3.0。组合发行必须遵守 AGPL 并提供对应源代码，不能用前端 MIT 给后端重新授权。Release 工作流计划附带同一 commit 的源码归档；素材授权未知仍阻止公开再分发。

`.github/workflows/integrated-release.yml` 仅 workflow_dispatch：默认不 publish，另需仓库变量 `INTEGRATED_RELEASE_APPROVED=true` 和 `integrated-release` environment 审批。维护者应配置必需审核人；没有审批保护就不能把 environment 名称当自动安全保证。仅固定版本，不推 `latest`；工作流配置未被本任务执行，镜像/Release 发布状态仍待确认。

历史 `icon-service/`、`deploy/icon-service/`、纯前端/双容器方案保留为回溯资料，均不是当前原生集成版的安装路径。
