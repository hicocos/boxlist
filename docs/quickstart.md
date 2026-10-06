# boxlist 部署指南

## 1. 启动

安装 Git、Docker Engine 和 Docker Compose 插件。目前公共镜像尚未发布，使用源码构建；仓库开放前需要有仓库访问权限。

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose up -d --build
```

访问 `http://服务器IP:5244`。初始管理员信息查看：

```sh
docker compose logs boxlist
```

登录后修改密码，不要公开含初始密码的日志。前端和图标接口已集成，无需另外复制前端或部署素材服务。

## 2. 域名与 HTTPS

1. 将自己的域名 DNS 解析到服务器。
2. 在反向代理中为域名配置 HTTPS 证书。
3. 将整站代理到 `http://127.0.0.1:5244`。
4. 在项目目录新建 `.env`：

```dotenv
SITE_URL=https://files.example.com
BIND_ADDRESS=127.0.0.1
PORT=5244
```

将示例域名换成自己的地址，执行：

```sh
docker compose up -d
```

从 HTTPS 域名访问后台 `/@manage/icons`。图标上传/删除要求真实管理员与 HTTPS 同源请求；不需要独立素材域名或额外 token。

反向代理应保留 Host、Authorization、Origin，让 `/api/`、`/icon/` 与页面保持同源，上传限制至少 6 MiB。不要给 `/icon/` 配置 SPA fallback，也不要将私有数据卷公开为静态文件。反代如果运行在另一个容器中，应通过共享 Docker 网络连接应用，而不是使用反代容器自己的 `127.0.0.1`。

## 3. 改端口

在 `.env` 设置 `PORT=5246`，执行 `docker compose up -d`。HTTP 访问地址和反向代理目标同步改为 5246。

默认监听所有宿主机接口，直接 HTTP 访问时应限制防火墙来源。采用宿主机反代后，设置 `BIND_ADDRESS=127.0.0.1` 可关闭应用端口的直接外部访问。

## 4. 数据与备份

默认使用 Compose 管理的 **Docker 数据卷**，自动完成初始化，无需手动建目录或修改权限。配置、数据库和图标数据共同保存在容器的 `/opt/openlist/data`。

在项目目录执行以下命令导出备份（备份文件可能包含账号和存储凭据，请妥善保管）：

```sh
docker compose stop boxlist
docker compose run --rm --no-deps --entrypoint tar boxlist \
  -czf - -C /opt/openlist/data . > boxlist-backup.tar.gz
docker compose start boxlist
```

检查备份命令成功且文件可读取。恢复时先停止应用，在待恢复的数据卷中解包，并保留文件权限；不要对运行中的数据库解包覆盖。

- `docker compose stop/start`：停止/启动，不删除数据。
- `docker compose down`：移除容器，保留数据卷。
- **`docker compose down -v`：删除数据卷，禁止作为日常更新命令。**

如需自己指定宿主机数据目录，可自行修改 Compose 挂载方式，并确保目录对镜像的 `1001:1001` 用户可写；这是可选高级配置，不是默认安装步骤。

## 5. 更新

先备份数据，再获取确认要使用的版本并重新构建：

```sh
git pull --ff-only
docker compose up -d --build
```

生产环境建议选择固定且已验证的 tag/commit，不盲目追踪开发分支。数据格式发生变化时，回滚需要对应版本的数据备份，不能只替换镜像。

已有其它部署的数据和图标接口不会自动迁移。请先备份，在副本上核对配置、图标选择和素材目录，再切换；默认命令不会连接其它实例的数据。

## 6. 发布版

公共 Release/GHCR 镜像尚未发布。发布后将提供固定版本镜像、预编译前端 `dist/`、对应源码与校验文件；以 [Releases](https://github.com/hicocos/boxlist/releases) 实际存在的版本为准。
