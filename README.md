# OpenList 二改 · 原生集成版

**当前方案：修改 OpenList Go 后端，在同一个 Go 进程、同一个 Docker 容器内提供二改前端与 `/icon/` 图标接口。不是纯前端，也不再要求 Python、Nginx 或独立图标容器。**

## 后端新增功能与改动范围（先看这里）

- 静态 **PNG 上传**：完整校验和实际解码，拒绝改后缀的其它格式及 APNG。
- **素材目录**与同源 **WebP 预览**；原图、数据库和回收数据不公开。
- **内置图标隐藏**、上传图标删除、持久化 **tombstone/私有回收数据**；隐藏内置项不删除源码素材。
- **容量/配额限制**：保留的删除数据也计入容量，不能靠删除无限绕过。
- 使用真实 OpenList **原生管理员鉴权**，复核账号禁用/登录状态；不提供鉴权绕过或单独凭据。
- 删除前读取最新 `customize_head`，拒绝删除已引用素材；读配置失败时 **fail closed**。这不是图标删除与所有设置 API 之间的跨请求事务。
- 新 **`/icon/` 路由**，与 `/api/`、前端共享站点；写操作要求实际 HTTPS 同源 Origin。

**后端源码基线：** [OpenListTeam/OpenList v4.2.6](https://github.com/OpenListTeam/OpenList/tree/v4.2.6)，tag commit `2bdf16d5967d0a403f67d809efd5a418b8f5bd30`。自定义内容集中在以下确切路径：

| 路径 | 用途 |
| --- | --- |
| `backend/internal/iconlibrary/` | 新增独立原生 Go 素材模块及测试（文件清单见 [后端变更](docs/backend-changes.md)） |
| `backend/server/icon_library.go` | 新增适配层：原生鉴权、HTTPS Origin、最新 head 读取、私有数据路径 |
| `backend/server/router.go` | 两行接入：注册图标路由，在静态 SPA fallback 之前生效 |
| `backend/go.mod`, `backend/go.sum` | Go 工具链/素材处理依赖 |
| `backend/public/dist/` | 构建阶段写入完整二改前端并嵌入二进制；不是手工维护的上游代码改动 |

没有修改存储驱动、账号/数据库模型或官方登录流程。升级时不要重写整份上游文件：保留独立模块，逐项重放最小路由接入与适配层，重新核对权限/API/静态嵌入行为并运行测试；[升级清单](docs/backend-changes.md#升级上游清单) 是合并依据。

## 安装：先确认版本是否真的发布

**目前没有在本说明中确认任何 Release 或 GHCR 镜像已经发布/公开。** 下方 `custom-vX.Y.Z` 是占位符，不是可用版本。仓库、镜像和素材授权审查完成前保持私有；不要把 `latest` 当可用发行版。

发布后，新用户可以下载对应版本的 `install.sh` 并一条命令安装：

```sh
# 将 custom-vX.Y.Z 换成 Releases 页面实际存在的版本；当前待发布。
VERSION=custom-vX.Y.Z
curl -fL "https://github.com/hicocos/openlist-frontend-custom/releases/download/$VERSION/install.sh" -o install.sh
# 先阅读脚本并核验同一 Release 的 SHA256SUMS，再运行：
sudo sh install.sh --version "$VERSION"
```

默认新建 `/srv/openlist-custom`，容器名 `openlist-custom`，端口 `5244`，目录所有者 UID/GID `1001:1001`。支持 `--dir`、`--port`、`--name`、`--image`、`--dry-run`。**已有目录/同名容器/端口冲突均拒绝，不递归 chown、不覆盖旧数据库。** 若已有官方实例，用全新目录/名称/端口试运行，不直接接管生产数据。

完整顺序、直接 `docker run`、HTTPS、备份和独立 `dist/` 下载方案见 **[快速开始](docs/quickstart.md)**。仅切换静态前端到官方后端不会凭空增加 `/icon/` 上传/删除接口。

## 前端二改

- 毛玻璃/透明度、壁纸和原生滚动行为。
- 文件类型图标管理、内置/上传素材、冲突合并和保存保护。
- 紧凑根目录搜索、取消/超时、结果保留。
- 长文件名/路径、多种布局、目录返回定位和选中交互。
- 图片预览、目录画廊、连续/单张阅读器、阅读进度。
- 手机视频时间/画中画与分组本地设置、移动工具栏。

## 本地构建（不部署）

```sh
# 项目根目录；需要可用 Docker Engine / BuildKit
# Go 1.27.0、Node 24.21.0、pnpm 11.24.0 已在 Dockerfile 固定。
docker build --build-arg VERSION=custom-local -t openlist-custom:custom-local .
```

根 `Dockerfile` 先编译同源前端，再复制 `dist/` 到 `backend/public/dist/` 后编译 Go。运行时直接执行 Go 二进制（PID 1），无后台 Python/Nginx/aria2。前端同时保留在镜像 `/opt/openlist/dist`；默认 `dist_dir` 留空即使用嵌入版本，无需改配置。若用户配置已有非空 `dist_dir`，它仍会覆盖内嵌版本，迁移时必须单独检查。

独立前端开发：

```sh
npm install -g pnpm@11.24.0
pnpm install --frozen-lockfile
pnpm run test:ux
pnpm run lint
VITE_API_URL=/ pnpm run build
```

`dist/` 必须完整包含 `index.html`、`assets/`、`static/`；不要只复制入口 JS。仓库已包含所需中文/英文语言文件，普通构建无需 Crowdin 凭据。Go 模块测试在 `backend/` 中执行 `go test ./internal/iconlibrary ./server`；隔离测试不等于真实管理员破坏性验收。

## 构建与发布状态

- 本地已实测：124 项前端测试、类型检查/构建、Go race 测试、完整镜像构建；全新隔离容器通过真实登录、上传/幂等/WebP、权限撤销、在用删除保护和重启持久化。详见 [验收记录](deploy/integrated/VALIDATION.md)。未替换现有实例，未发布公共版本。
- `Validate and build` 是源码验证工作流，具体内容以 `.github/workflows/build.yml` 为准。
- 新增 `.github/workflows/integrated-release.yml`：**仅手动启动**。默认仅构建并保存私有 Actions artifacts；显式选择发布并通过 `integrated-release` environment 审批后才允许上传指定版本 Release/GHCR。配置文件存在不表示发布已经执行。
- 计划提供完整预编译 `dist/` 包和 SHA256SUMS，用户不必自己编译前端。静态包是替代部署形式，不是完整后端安装包。

## 仓库结构与旧方案

- `src/`, `public/`, `patches/`, `tests/`：前端源码和测试。
- `backend/`：AGPL-3.0 的 OpenList Go 后端基线及独立素材扩展。
- `Dockerfile`, `.dockerignore`, `scripts/install.sh`, `deploy/integrated/`：当前单容器原生集成方案。
- `icon-service/`, `deploy/icon-service/`：**历史 Python sidecar**，仅为旧部署回溯保留，不进入根镜像运行时。
- `docs/install.md`, `docs/icon-service.md`, `docs/simple-deployment-plan.md`, `docs/two-container-plan.md`：**历史纯前端/独立服务/双容器说明，不是当前集成版安装入口**；与当前方案冲突时以本 README、`docs/quickstart.md`、`docs/backend-changes.md` 为准。

仓库不包含站点登录凭据、生产配置、存储账号、数据库、上传素材或浏览器进度。打包/验证/发布源码不会自动改动现有运行实例。

## 来源与授权

前端基线 [OpenListTeam/OpenList-Frontend v4.2.6](https://github.com/OpenListTeam/OpenList-Frontend)，commit `0725e589bb7f0e734a7446f427eed2175f26c15c`。根 [LICENSE](LICENSE) 继续保留 **前端上游 MIT**，不能把它解释为整个集成后端均为 MIT。后端 [backend/LICENSE](backend/LICENSE) 为 **AGPL-3.0**；包含修改后端的组合发行须遵守 AGPL，包括提供对应源代码及适用的网络使用义务。第三方依赖按各自许可证；集成镜像保留两份许可证。

现有图片/字体等素材的再分发授权尚未逐一确认。代码许可证不自动覆盖所有素材；**公开仓库/Release/GHCR 前必须完成授权与历史凭据审查**。部分预览及用户配置外部资源仍依赖 CDN，不能承诺完全离线。
