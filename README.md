# boxlist v1.0

基于 [OpenList](https://github.com/OpenListTeam/OpenList) 的多存储文件列表程序，提供文件管理、在线预览、图片阅读和界面自定义。前端与 Go 后端由 GitHub Actions 提前构建为镜像，部署时直接下载并启动一个容器，无需在服务器上编译。

[部署指南](DEPLOYMENT.md) · [预览图使用说明](doc/thumb-operations.md) · [问题反馈](https://github.com/hicocos/boxlist/issues)

## 功能

- **多存储接入**：本地目录、网盘、对象存储，以及 WebDAV、FTP、SFTP、SMB 等存储驱动。
- **文件管理**：上传、下载、搜索、重命名、移动、复制、删除、打包下载及离线下载；具体能力取决于驱动、权限和相关服务配置。
- **预览与阅读**：图片画廊、连续/单张阅读、阅读进度记忆；音视频、PDF、Markdown、代码、纯文本及 Office 文档预览。
- **预览图任务**：管理员按目录生成列表缩略图和阅读预览，支持任务控制、显式启用的自动规则、本地或 [S3 预览图存储](doc/derivative-s3.md)。不修改原图。
- **访问控制**：用户权限、目录密码、分享和 WebDAV 服务。
- **界面自定义**：深浅色、多语言、移动端适配、壁纸、透明度及 PNG/GIF 图标素材管理。附带 34 款可删除的内置素材，也支持自行上传。

## 安装

需要 Docker Engine 和 Docker Compose 插件，以及访问 GHCR（`ghcr.io`）的网络。镜像支持 Linux AMD64 / ARM64，Docker 会自动选择对应架构。

```sh
mkdir -p boxlist
cd boxlist
curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/compose.yaml -o compose.yaml
docker compose pull
docker compose up -d
```

访问服务器的 **5244** 端口。默认映射到全部网络接口；正式对外开放前请配置 HTTPS、修改管理员密码并检查访客权限。

默认容器名为 `boxlist`，镜像为 `ghcr.io/hicocos/boxlist:latest`。`latest` 跟随通过构建和启动验证的 `main` 分支；需要固定版本时，在 `.env` 中设置 `BOXLIST_IMAGE=ghcr.io/hicocos/boxlist:sha-完整提交SHA`，或使用已经发布的版本标签。可用镜像见 [GitHub Packages](https://github.com/hicocos/boxlist/pkgs/container/boxlist)。

首次启动的管理员密码输出到容器日志：

```sh
docker compose logs boxlist
```

日志包含敏感信息，请勿公开。登录后在管理页面添加自己的存储；仓库不预置站点账号、存储挂载或用户文件。

## 配置

项目根目录的 `.env` 可设置以下 Compose 变量：

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `SITE_URL` | 空 | 站点对外访问的完整地址；使用反向代理时设置为实际 HTTPS 地址 |
| `BIND_ADDRESS` | `0.0.0.0` | 宿主机端口绑定地址；同机反向代理建议 `127.0.0.1` |
| `PORT` | `5244` | 宿主机端口；容器内部仍为 `5244` |
| `BOXLIST_IMAGE` | `ghcr.io/hicocos/boxlist:latest` | 镜像地址，可指定已发布标签或 digest |

修改后运行 `docker compose up -d` 使容器配置生效。其他应用环境变量需要在 Compose 的 `environment` 中显式传入，不能仅写入 `.env`。

本地目录映射、反向代理、构建版本、备份恢复及功能限制见 [部署指南](DEPLOYMENT.md)。

## 更新与数据安全

先备份应用数据、挂载的原文件及外部服务数据，再拉取镜像并重新创建容器：

```sh
docker compose pull
docker compose up -d
```

- 默认使用命名卷 `data`，挂载到 `/opt/openlist/data`；重建容器不等于重建数据。
- **不要用 `docker compose down -v` 更新**：它会删除命名卷。更换 Compose 项目名或卷映射也可能让应用连接到空卷。
- 数据库、配置、图标和本地预览图应一起备份；启用 S3 预览图时还要保留加密密钥及桶对象。
- 网页导出的配置/索引备份不等于完整数据备份，也不包含原文件。具体范围见 [备份与恢复](DEPLOYMENT.md#备份与恢复)。

已有源码部署迁移时，在原项目目录运行 `git pull --ff-only`，保留原项目名、数据卷和本地配置，再执行上面的两条命令。Compose 会把自动生成的容器名更新为 `boxlist`；默认数据卷仍是 `boxlist_data`。如使用了自定义部署文件，应同步改为镜像部署，详见[部署指南](DEPLOYMENT.md#从源码部署迁移)。

## 从源码构建

只有修改源码或需要自行构建时才使用此方式，需要 Git 和构建依赖源网络。首次编译仍会消耗较多时间和内存：

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

后续源码更新、管理员命令和其他 Compose 操作也应使用同样的两个 `-f` 参数，避免切回预构建镜像。构建与发布流程见[部署指南](DEPLOYMENT.md#镜像发布)。

## 使用边界

媒体播放取决于浏览器编码支持；Office 预览可能依赖外部服务，私密文件应谨慎选择预览方式。大文本分页减少页面渲染量，但仍完整下载文件到浏览器内存。预览图只处理受支持的静态图片，缺失或不适用时使用原图；生成规则不会因访客浏览自动启用。

GIF 图标的“生成透明背景”会覆盖原素材且**无法恢复原图**，请先保存副本。自定义 HTML/脚本只应来自可信来源。

内置图标在首次安装时导入素材库，默认不自动应用到文件类型。管理员可像上传素材一样删除、选用或处理 GIF；已使用的素材需先更换映射并保存。删除记录持久化，重启或升级不会自动恢复已删除素材。

## 反馈与贡献

欢迎通过 [Issues](https://github.com/hicocos/boxlist/issues) 和 Pull Request 提交问题或改进。报告问题时附上源码版本、部署方式、浏览器、复现步骤及脱敏日志；不要公开密码、令牌、配置备份或带签名的下载地址。

## 许可证

本项目基于 OpenList 及其前端开发。前端采用 [MIT](LICENSE)，后端采用 [AGPL-3.0](backend/LICENSE)；第三方组件和素材遵循各自许可证。分发或通过网络提供修改后的服务时，请遵守相应许可证的源码提供等要求。
