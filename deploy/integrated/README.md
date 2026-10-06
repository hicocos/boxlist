# boxlist 发布镜像部署

公共镜像尚未发布。当前推荐按 [部署指南](../../docs/quickstart.md) 使用项目根目录的 Compose 从源码构建。

本目录的 Compose 用于已发布的固定镜像版本。发布后，在项目根目录的 `.env` 中设置 `BOXLIST_IMAGE=ghcr.io/hicocos/boxlist:实际版本`，运行：

```sh
docker compose --env-file .env -f deploy/integrated/compose.yaml up -d
```

采用 Docker 数据卷，不需要手动建目录或修改权限。域名、端口与 HTTPS 设置仍使用 `SITE_URL`、`PORT`、`BIND_ADDRESS`，与部署指南一致。源码构建版和发布镜像版二选一，不要同时启动。
