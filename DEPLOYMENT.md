# 部署与运维

推荐使用 Docker Compose，支持 Linux AMD64 / ARM64。镜像包含前端、后端和预览图工具，无需在服务器上编译。

## 一键安装与管理

```sh
curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/install.sh -o install-boxlist.sh
sudo bash install-boxlist.sh install --install-docker
```

选择 `1` 安装，端口默认 `5244`。安装目录默认为 `/opt/boxlist`。安装后运行 `sudo boxlist` 或 `sudo boxlist-manager` 打开管理菜单。

| 命令 | 用途 |
| --- | --- |
| `sudo boxlist update` | 更新程序，保留配置和数据 |
| `sudo boxlist status` / `logs` | 查看状态 / 日志 |
| `sudo boxlist start` / `stop` / `restart` | 启动 / 停止 / 重启 |
| `sudo boxlist password` | 重置管理员密码 |
| `sudo boxlist configure` | 修改端口和站点地址 |
| `sudo boxlist backup` / `restore` | 备份 / 恢复应用数据 |
| `sudo boxlist script-update` | 更新管理脚本 |
| `sudo boxlist uninstall` | 卸载并删除应用数据 |

卸载会删除安装目录及其中的备份，请先将需要保留的文件移至其他位置。其他选项见 `bash install-boxlist.sh --help`。

## Docker 部署

将 [compose.yaml](compose.yaml) 下载到独立目录后启动：

```sh
mkdir -p boxlist
cd boxlist
curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/compose.yaml -o compose.yaml
docker compose pull
docker compose up -d
```

访问 `http://服务器IP:5244`。全新安装在网页初始化向导中创建管理员账号，已有数据继续使用原账号密码。请在公开站点前完成初始化，并使用强密码。

默认禁用访客。登录管理后台添加存储，并按需要配置用户和访问权限。

自动化部署可在 Compose 的 `environment` 中显式设置 `OPENLIST_ADMIN_PASSWORD`，仅在没有管理员时创建 `admin`，不会重置已有密码。该方式首次启动会输出密码，不要公开日志。

忘记密码时，在原部署目录执行：

```sh
docker compose stop boxlist
docker compose run --rm --no-deps boxlist admin random --no-prefix
docker compose up -d boxlist
```

## 配置与网络

在部署目录的 `.env` 中配置：

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `SITE_URL` | 空 | 对外访问地址，建议使用 HTTPS |
| `BIND_ADDRESS` | `0.0.0.0` | 宿主监听地址 |
| `PORT` | `5244` | 宿主端口 |
| `BOXLIST_IMAGE` | `ghcr.io/hicocos/boxlist:latest` | 镜像标签或 digest |
| `BOXLIST_CONTAINER_NAME` | `boxlist` | 容器名称 |

同机反向代理示例：

```dotenv
SITE_URL=https://files.example.com
BIND_ADDRESS=127.0.0.1
PORT=5244
```

建议使用 Nginx 或 Caddy 配置 HTTPS。代理应保留 Range、HEAD 和 WebDAV 方法，按需求设置上传大小和超时；不要公开应用数据目录，也不要缓存受权限保护的 API 或预览图。日志中的下载签名、令牌和密码需脱敏。

其他环境变量通过 `compose.override.yaml` 显式传入：

```yaml
services:
  boxlist:
    environment:
      TZ: Asia/Shanghai
      DERIVATIVES_BUDGET_BYTES: "21474836480"
```

修改 `.env` 或 Compose 配置后运行 `docker compose up -d`。仅重启容器不会应用新的环境配置。

## 数据卷与本地存储

默认数据卷为 `boxlist_data`，容器路径为 `/opt/openlist/data`。容器使用 **UID/GID 1001:1001**。配置、数据库、图标、预览图和密钥都保存在数据卷中。

本地原文件需单独挂载，例如：

```yaml
services:
  boxlist:
    volumes:
      - /srv/boxlist-files:/mnt/files:ro
```

为 UID/GID 1001 提供必要的读取权限，在管理后台添加 Local 存储并填写容器路径 `/mnt/files`。需要上传或修改时再移除 `:ro` 并授予写权限，不要使用全局可写权限。

不要让多个后端进程共用同一数据目录。切换卷或绑定目录前先停止服务并迁移数据，空目录会被视为全新安装。

## 更新与回退

更新前备份数据，并记录当前镜像和卷映射。

一键脚本部署使用 `sudo boxlist update`。Docker 部署在原目录执行：

```sh
docker compose pull
docker compose up -d
```

固定版本的实例需先修改 `BOXLIST_IMAGE`。不要使用 `docker compose down -v` 更新，也不要更换项目名或数据卷。已有账号和密码不会因更新重置。

回退时选择先前的镜像标签或 digest，并先确认数据兼容性。需要恢复旧数据库时，先另存当前数据，避免丢失更新后产生的内容。

## 备份与恢复

1. 停止服务，确保没有其他程序写入数据。
2. 备份整个应用数据卷并保留文件权限，不要只复制正在运行的 SQLite 数据库。
3. 单独备份本地原文件、远端文件和 S3/R2 桶对象。
4. 恢复服务，将备份放在受限且独立的位置。

恢复时先停止服务，还原数据和 UID/GID 1001 的访问权限，再检查管理员登录、存储与文件读取。

**网页备份不等于完整数据卷备份。** 预览图备份不包含图片文件；原始数据库中的对象存储凭据依赖 `derivatives/s3.key`，二者必须一同保存。网页备份也可能含可用凭据，请设置密码，勿上传到公开仓库或问题反馈中。

## 自行源码构建

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

后续管理和更新继续使用相同的两个 `-f` 参数。工具链与依赖版本以 `Dockerfile`、`package.json`、`backend/go.mod` 为准。

## 使用提示

- 预览图生成与限制见 [预览图使用说明](doc/thumb-operations.md)，对象存储配置见 [S3 / R2 说明](doc/derivative-s3.md)。
- 图标管理的写操作需要 HTTPS，并将 `SITE_URL` 设为实际站点地址。GIF 去白底会覆盖素材，操作前保留原图。
- 自定义 HTML 或脚本只使用可信内容。部分文档预览依赖第三方服务，敏感文件使用前请了解其数据处理方式。
- 推荐前后端同源部署。分离部署时，SSO 的 `sso_postmessage_origin` 填前端 origin，不含路径；账号绑定还受浏览器 cookie 策略限制。
