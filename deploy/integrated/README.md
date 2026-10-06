# 原生集成部署文件

当前方案只运行一个 Go 进程。此目录不提供 Python/Nginx sidecar；`../icon-service/` 为历史资料。

`compose.yaml` 是安装脚本的替代方式，不要同时执行两种安装。需要先准备新的绝对数据目录并仅对该新目录设置 UID/GID `1001:1001`、权限 `0750`。已存在的数据库目录属于另行审批的迁移范围。

```sh
# 全部是示例，占位版本待发布；不要直接当成可用镜像。
export OPENLIST_IMAGE=ghcr.io/hicocos/openlist-custom:custom-vX.Y.Z
export OPENLIST_DATA_DIR=/srv/openlist-custom
export OPENLIST_PORT=5246
export OPENLIST_SITE_URL=https://files.example.com # 替换实际自己的 HTTPS 站点
# 无副作用语法/变量检查（不启动、不拉镜像）：
docker compose -f deploy/integrated/compose.yaml config --quiet
# 仅在版本存在、目录与端口审查后主动执行：
docker compose -p openlist-custom -f deploy/integrated/compose.yaml up -d
```

默认只绑定回环地址供已有 HTTPS 反代使用。`create_host_path: false` 防止 Compose 自动创建 root 所有目录。默认生成的 Compose 项目名称可由 `-p` 显式隔离，避免和旧实例混淆。配置检查成功不代表镜像发布、HTTP 可用或管理员验收通过。

完整步骤、权限和数据回滚见 [快速开始](../../docs/quickstart.md)。
