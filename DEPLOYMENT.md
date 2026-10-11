# 部署与运维

本指南对应仓库的 [`compose.yaml`](compose.yaml)、可选的 [`compose.build.yaml`](compose.build.yaml) 和 [`Dockerfile`](Dockerfile)。不依赖已有实例、预置账号、样本目录或额外本地脚本。

## 部署模型与构建要求

推荐使用 Linux 容器及 Docker Compose 插件。默认从 GHCR 拉取提前构建的镜像，支持 AMD64 / ARM64；部署服务器不需要下载源码、安装构建依赖或编译。镜像包含本仓库的前端和 Go 后端，以单个服务进程运行。`latest` 跟随成功发布的 `main` 分支，固定版本可设置 `BOXLIST_IMAGE` 为已发布标签或 digest。

以下是镜像发布或自行源码构建时的配置，普通镜像部署无需执行这些步骤：

| 项目 | 版本或配置来源 |
| --- | --- |
| Node.js | Dockerfile：`24.21.0`，Debian Bookworm |
| pnpm | package.json / Dockerfile：`11.24.0`，使用冻结的锁文件安装 |
| Go | Dockerfile / go.mod toolchain：`1.27.0`；go.mod 语言版本为 `1.25.0` |
| 前端构建 | `VITE_API_URL=/ pnpm run build` |
| 后端构建 | `CGO_ENABLED=0`，`jsoniter` 构建标签，嵌入 `backend/public/dist` |
| 运行基础镜像 | `debian:bookworm-slim` |
| 图片编码器 | `libvips-tools=8.14.1-3+deb12u3`，由运行镜像安装，另需 `util-linux` 的资源限制工具 |

Docker 构建不要求宿主机安装 Node、pnpm、Go 或 libvips，但需要网络、足够的磁盘和构建内存。Dockerfile 使用 BuildKit 缓存挂载；使用支持它的 Docker Engine。源代码构建与图片处理的资源需求不同，不应把某台机器的内存配额视为通用最低要求。当前 Compose 没有设置 CPU/内存硬上限，应按负载配置。

如自行构建后端，必须先构建本仓库前端并将 `dist/` 内容放入 `backend/public/dist/`，再编译 Go。Linux 运行环境还需安装上述图片工具；仅构建 Go 二进制不能提供预览图编码能力。

## 首次启动

```sh
mkdir -p boxlist
cd boxlist
curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/compose.yaml -o compose.yaml
docker compose pull
docker compose up -d
docker compose logs boxlist
```

服务名和容器名均为 `boxlist`，默认镜像为 `ghcr.io/hicocos/boxlist:latest`。首次创建管理员时密码只输出到控制台，之后数据库保存的是哈希；不要把启动日志作为公开故障报告上传。

访问服务器的 5244 端口，登录后修改密码、添加存储并检查访客/用户权限。若丢失密码，可停止服务后通过同一数据卷运行密码重置命令，避免另起一个服务进程：

```sh
docker compose stop boxlist
docker compose run --rm --no-deps boxlist admin random --no-prefix
docker compose up -d boxlist
```

重置输出包含新密码，请妥善保存并及时修改。

## 配置与网络

Compose 从项目根目录 `.env` 或命令行环境读取：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SITE_URL` | 空字符串 | 实际对外访问的完整站点地址；反向代理场景使用实际 HTTPS 地址 |
| `BIND_ADDRESS` | `0.0.0.0` | 宿主端口绑定的接口 |
| `PORT` | `5244` | 宿主端口，转发到容器内 `5244` |
| `BOXLIST_IMAGE` | `ghcr.io/hicocos/boxlist:latest` | 可指定已发布版本标签、`sha-完整提交SHA` 或 `ghcr.io/hicocos/boxlist@sha256:…` |

例如，仅供同机反向代理访问：

```dotenv
BIND_ADDRESS=127.0.0.1
PORT=5244
```

另将 `SITE_URL` 设置为自己的公开 HTTPS 地址。默认全部接口绑定会让局域网或公网直接访问端口，需同时考虑防火墙和云安全组。

`BIND_ADDRESS` 和 `PORT` 是 Compose 的端口映射变量，不会修改后端监听配置。应用配置保存在数据卷内的 `config.json`，默认 SQLite 数据库为 `data.db`。当前启动参数使用 `--no-prefix`；应用环境变量通常覆盖配置文件对应项，但 `config.json` 的 `force: true` 会禁止此覆盖。管理页面中的站点、存储和权限设置则保存在数据库。

默认 Compose 只向容器传入 `SITE_URL`。其他变量（例如预览图限制或 `TZ`）需通过 Compose `environment` 显式传入。可以使用本地 `compose.override.yaml`，避免直接改动上游文件：

```yaml
services:
  boxlist:
    environment:
      DERIVATIVES_BUDGET_BYTES: "21474836480"
```

此示例将本地预览图与临时工作预算设为 20 GiB，不代表默认配额。修改 Compose 或 `.env` 后运行 `docker compose up -d` 重新创建受影响容器；`docker compose restart` 不会重新加载 Compose 环境定义。修改数据卷中的启动配置后需重启服务。

### 反向代理与 HTTPS

建议由 Nginx、Caddy 等在站点根路径终止 TLS，再代理至 Boxlist。证书申请、域名 DNS 和证书续期由部署者管理，镜像不会自动配置这些服务。

下面是放入**已经配置好域名、HTTPS 监听与证书的 Nginx `server` 块**中的位置配置；不是完整 TLS 配置：

```nginx
location / {
    proxy_pass http://127.0.0.1:5244;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_request_buffering off;
    proxy_buffering off;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
    client_max_body_size 1g;
    access_log off;
}
```

- 按实际上传大小和网络状况调整大小限制及超时；外层 CDN/代理也可能有自己的限制。
- 保留 Range、HEAD、条件请求和 WebDAV 方法，不要把应用 API 改写为静态文件路由。
- 不要缓存受权限保护的 API 和预览图，也不要把 `data/` 或 `derivatives/artifacts/` 配置成公开静态目录。
- 下载/预览 URL 可能含短期票据或签名。上例关闭访问日志；如需日志，应使用不记录查询串的格式，并检查代理、CDN、错误日志与监控系统的脱敏策略。
- 若代理也运行在容器内，代理中的 `127.0.0.1` 指向它自己。应将两者接入受控 Docker 网络并代理至 `boxlist:5244`，不要照搬同机代理地址。

## 数据卷与本地存储

默认命名卷的 Compose 键为 `data`，挂载到 `/opt/openlist/data`；默认项目名为 `boxlist`，Docker 卷名通常为 `boxlist_data`。容器以 **UID/GID 1001:1001** 运行，工作目录为 `/opt/openlist`。

这个卷保存配置、数据库、图标、本地预览图、S3 凭据加密密钥及运行文件，不是已经挂载到应用中的全部原文件。网盘/S3 中的原文件留在对应服务；本地文件需要另加绑定挂载。

例如在本地 Compose override 中添加一个只读目录：

```yaml
services:
  boxlist:
    volumes:
      - /srv/boxlist-files:/mnt/files:ro
```

先创建并确认宿主目录，赋予 UID/GID 1001 所需的读取和目录遍历权限，再在管理后台添加 Local 存储、填写**容器路径** `/mnt/files`。只有确实需要上传/修改时才去掉 `:ro` 并授予写权限；应用内权限和文件系统权限都必须允许该操作。

如把应用数据卷改为宿主绑定目录，也必须预先创建目录并设置 1001:1001 所有权和受限权限，不要用全局可写权限解决问题。切换挂载前先停止服务并迁移原卷内容；直接换成空目录会呈现全新实例。不要让多个后端进程共享同一数据库和预览图目录，预览图任务不是分布式工作队列。

## 更新与回退

更新前记录当前镜像 digest 和卷映射，完成一致性备份，再执行：

```sh
docker compose pull
docker compose up -d
docker compose logs --tail 100 boxlist
```

镜像部署不需要 `git pull` 或 `--build`。如果固定了版本标签或 digest，先修改 `.env` 中的 `BOXLIST_IMAGE`。不要用 `docker compose down -v` 更新；不要随意更换 Compose 项目名或数据卷。

回退时将 `BOXLIST_IMAGE` 设置为先前记录的镜像 digest，再执行上述命令；先在数据副本上确认兼容性。旧程序不一定理解新增数据库字段或 S3 预览图；也不要为了回退直接覆盖数据库而丢失更新后的用户、设置和任务记录。

### 从源码部署迁移

在原项目目录执行 `git pull --ff-only` 获取新的 Compose 配置，再执行 `docker compose pull` 和 `docker compose up -d`。有本地改动时先处理合并，不要强制覆盖。默认项目名仍为 `boxlist`，卷仍为 `boxlist_data`，挂载点仍为 `/opt/openlist/data`，UID/GID 仍为 1001:1001。

容器名由 Compose 自动生成的 `boxlist-boxlist-1` 改为 `boxlist`；同一项目内 `up -d` 会重建该服务并保留数据卷。不要手动新建另一个项目或空卷。如已有其他项目占用容器名 `boxlist`，先确认归属并在本地 override 中选择其他名称；显式容器名意味着单台 Docker 主机上多实例需使用不同名称。

使用自定义 `compose.deploy.yaml` 等文件的实例，需在原文件中将 `image` 改为 GHCR 镜像、移除 `build` 并设置 `container_name: boxlist`；保留原有挂载、环境、端口和资源限制，继续使用原来的 `-f` 参数及项目名。不要直接替换成默认配置，否则绑定目录可能变成空命名卷。

### 自行源码构建

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

构建覆盖文件使用 `boxlist:local`，禁止拉取同名远端镜像。可以设置 `BOXLIST_VERSION` 和 `SOURCE_COMMIT` 作为构建版本信息。更新源码后重新执行相同构建命令；后续日志、停止、管理员命令等操作也使用同样的两个 `-f` 参数。不要只运行默认 `docker compose up`，否则会切换回 GHCR 镜像。

### 镜像发布

[`publish-image.yml`](.github/workflows/publish-image.yml) 在推送 `main`、推送 `v*` 标签或手动运行时构建镜像。两个原生 GitHub runner 分别构建 AMD64 和 ARM64，使用按架构隔离的 BuildKit 缓存，启动临时容器检查首页和公开设置 API；两种架构均通过后才发布多架构标签。

- `main` 发布 `latest` 和 `sha-完整提交SHA`。
- `v*` 标签发布同名镜像标签及提交标签，不覆盖 `latest`；手动运行根据所选分支/标签遵循同样规则。
- 工作流使用仓库的 `GITHUB_TOKEN`，需要 Actions 可运行且允许 `packages: write`，无需另存个人令牌。
- GHCR 新建包可能默认为私有。首次发布后，在 GitHub 的 `Packages → boxlist → Package settings` 中将可见性设为 Public，才能让普通用户免登录拉取。公开源码仓库不自动保证容器包公开。
- 首次发布完成并确认匿名拉取成功后再使用镜像部署；若提示 `denied`，检查包可见性与访问权限；若提示 `manifest unknown`，检查工作流是否成功及标签是否存在。源码构建可在发布前使用。
- Fork 会发布到自己的小写仓库路径，部署时需通过 `BOXLIST_IMAGE` 指向自己的包。

发布状态见 [Actions](https://github.com/hicocos/boxlist/actions/workflows/publish-image.yml)，可用标签见 [Packages](https://github.com/hicocos/boxlist/pkgs/container/boxlist)。

## 备份与恢复

推荐的完整备份步骤：

1. 暂停维护操作，执行 `docker compose stop boxlist`，确认没有其他写入者。
2. 使用 Docker 卷备份工具或临时容器归档**整个应用数据卷**，保留文件权限；不要只复制运行中的 `data.db` 而忽略 SQLite WAL。外部数据库应使用数据库原生一致性备份。
3. 单独备份本地原文件、远端原文件及 S3 预览图桶对象；应用数据卷不包含它们。保存实际 Compose override、环境配置和版本信息，这些文件可能含密钥。
4. 执行 `docker compose up -d boxlist` 恢复服务，将备份存放在访问受限且独立于当前数据卷的位置。

恢复时先停止服务，将数据还原到正确的卷或目录，恢复 UID/GID 1001 的访问权限，再启动匹配版本。先在隔离环境确认管理员登录、存储挂载和文件读取，避免把空卷或不完整备份直接作为正式数据。

**网页“备份与还原”不替代完整卷备份。** 其中预览图部分只导出 S3 配置、可迁移凭据和索引，不包含本地预览图、任务、规则或任何图片文件。原始数据库备份必须同时保留 `derivatives/s3.key`；丢失它无法解密原数据库中的 S3 凭据。网页备份含可迁移凭据，应设置备份密码并妥善保存。详情见 [S3 备份说明](doc/derivative-s3.md#备份与恢复)。

## 功能边界与使用提示

### 预览图

管理员手动生成或明确启用目录规则，访客 GET 请求不会触发生成。支持的静态格式、大小上限、工作并发、空间保护和清理入口见 [预览图运维](doc/thumb-operations.md)。生成不改写原图；S3/CDN 直读有不同的保密边界，启用前阅读 [S3 说明](doc/derivative-s3.md)。

### 文本、媒体和文档

- 默认不超过 64 KiB 的文本文档使用 Markdown 渲染器，较大的使用分页文本阅读器；显式预览选择和管理员的“默认下载”设置优先。
- 分页保留全文和换行，浏览器查找只覆盖当前页。分页限制渲染量，不限制下载量：文件仍完整进入浏览器内存。
- 通用视频播放器对网络错误最多自动重试三次；解码失败、不支持格式或加载中止不会通过自动重试修复。播放能力取决于浏览器支持的容器/编码，不能视为通用服务器转码。
- Office 等预览方式可能需要第三方服务或可访问的文件链接。私密内容应先了解所选服务的数据处理方式；不要为预览方便盲目公开文件。
- 文件操作、搜索、跨存储复制和离线下载取决于存储驱动、权限及相关服务，不保证每个驱动都具有相同能力。

### 图标和界面自定义

- 图标素材支持 PNG/GIF，每张最多 5 MB，每批最多 20 张；不支持 APNG。GIF 保留动画，限制为最多 2000 帧、宽高各 4096 像素、画布 1600 万像素、画布面积与帧数乘积 1.6 亿。
- v1.0 默认导入 34 款内置 PNG/GIF 素材；不会自动修改图标映射。内置素材和上传素材使用相同的管理流程，可删除或处理 GIF 白底。删除状态保存在图标数据库，重启与升级不会重新补齐已删除素材；清空整个数据卷属于全新安装，会重新初始化。
- 图标素材的写操作要求管理员登录及匹配站点的 HTTPS Origin。正式使用上传、删除或去白底前，请配置 HTTPS 反向代理，并将 `SITE_URL` 设为实际访问地址；直接通过 HTTP 端口访问不满足该安全要求。
- 图标映射修改是草稿，点击“保存更改”才生效；素材上传、删除和去白底即时生效。已使用的素材需先解除引用再删除。
- GIF 的“生成透明背景”去除与边缘连通的白色区域，成功后原位覆盖素材，**没有恢复原图功能**。不是通用智能抠图，调色板和二值透明可能影响边缘与颜色；操作前保留原文件。
- 壁纸、公告和自定义 HTML 都由部署者配置。不要粘贴不可信脚本；自定义 body 在主内容就绪后激活，仅依赖 `DOMContentLoaded` 的脚本需适配延迟执行，需要启动前执行的内容应放在 head。
- 公告按浏览器标签页会话去重；恢复浏览器会话可能保留记录，禁用会话存储时只在当前页面生命周期去重。
