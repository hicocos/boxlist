# 快速开始：原生单容器版

**发行状态：待发布。** 下面的 `custom-vX.Y.Z` 是占位符。必须先在本仓库 Releases 确认版本和对应 GHCR 镜像实际存在；私有资源需要自己合法的 GitHub/GHCR 登录。不要把本文当成镜像已公开的声明。

## 1. 新安装（不接管已有实例）

准备 Linux、Docker Engine、root/sudo、`ss`（iproute2）和 `realpath`。默认端口 5244 若已有实例占用，选 5246；数据目录和容器名称必须全新。

```sh
VERSION=custom-vX.Y.Z # 替换成实际已发布版本，当前待发布
curl -fL "https://github.com/hicocos/openlist-frontend-custom/releases/download/$VERSION/install.sh" -o install.sh
curl -fL "https://github.com/hicocos/openlist-frontend-custom/releases/download/$VERSION/SHA256SUMS" -o SHA256SUMS
# 查看脚本、用 SHA256SUMS 中 install.sh 对应行核对 sha256sum install.sh。
sh install.sh --help
sudo sh install.sh --version "$VERSION" --dir /srv/openlist-custom --port 5246 --name openlist-custom --dry-run
sudo sh install.sh --version "$VERSION" --dir /srv/openlist-custom --port 5246 --name openlist-custom
```

从同一 Release 下载的校验文件能检测传输/文件不匹配，不独立证明发行者身份；先确认仓库与版本来源。若已下载并审查脚本，安装本身仅需最后一条命令。

脚本先检查 Docker、现有容器、监听/映射端口、目录；镜像拉取成功才创建目录，只对新目录非递归设置 `1001:1001` / `0750`。绝不停止旧容器、删除旧目录、递归修改生产权限或自动迁移数据库。失败时保留新目录供检查，重试前手动确认残留，不能绕过已有目录保护。

可用 `--image openlist-custom:custom-local` 指定固定标签，但安装器会执行 `docker pull`；本地构建镜像应使用下方直接运行方式（不伪装为已发布资源）。

## 2. 不用脚本，直接 Docker

```sh
# 必须是新的目录；mkdir 不用 -p，避免悄悄采用旧数据。
sudo mkdir -m 0750 /srv/openlist-custom
sudo chown 1001:1001 /srv/openlist-custom
# custom-vX.Y.Z 当前待发布；实际镜像固定版本来自 Releases。
docker run -d --name openlist-custom --restart unless-stopped \
  -p 5246:5244 \
  --mount type=bind,src=/srv/openlist-custom,dst=/opt/openlist/data \
  ghcr.io/hicocos/openlist-custom:custom-vX.Y.Z
```

公开端口绑定全部接口。只由本机反代访问时建议改为 `-p 127.0.0.1:5246:5244`，并用防火墙控制暴露。容器默认非 root，UID/GID **1001:1001**；宿主机 bind mount 必须可写。不要对现有站点执行 `chown -R`。

只启动一个 Go 进程；默认从二进制嵌入完整前端，镜像也提供 `/opt/openlist/dist`。默认 `dist_dir` 空即可使用内嵌版，不需要 Nginx/Python 启动脚本，也没有额外图标监听端口。WebDAV 及其它可选协议的端口不默认暴露，按实际配置另行评估。

## 3. 首次登录与 HTTPS（图标写操作必需）

1. `docker logs openlist-custom` 检查启动和初始管理员信息；日志可能含初始密码，不要公开粘贴。
2. 访问 `http://服务器IP:5246/` 完成初始登录，立即修改管理员密码。HTTP 可用于首次访问，**不能用来验收图标上传/删除**。
3. 自己的域名 DNS 解析到服务器，在宝塔或其它已有反向代理为该域名申请证书、启用 HTTPS。
4. 反向代理整站到 `http://127.0.0.1:5246`，保留 Host、Authorization、Origin；让 `/api/`、`/icon/`、预览与页面同源，不给 `/icon/` 设置 `try_files` SPA fallback，不把整个私有 data 暴露成静态目录。上传 body limit 至少 6 MiB。
5. 停止 **这个新容器** 后，在它的 `/srv/openlist-custom/config.json` 里只把 `site_url` 改成自己的规范 HTTPS URL，例如 `https://files.example.com`，保留全部其它字段并重启这个容器。该示例域名不是本仓库提供的服务。也可在直接 `docker run` / Compose 中显式配置 `-e SITE_URL=https://自己的域名`。
6. 在 `https://自己的域名/@manage/icons` 用真实管理员测试：目录读取、有效 PNG 上传、预览、保存选择、已引用素材删除被拒绝、解除引用后删除、内置项隐藏、重启后目录状态持久化。

安全边界：写操作要求 HTTPS 同源 Origin 和真实启用管理员；`SITE_URL` 应指向自己实际使用的 HTTPS 站点。没有第二个服务域名、额外 token、鉴权旁路或 CORS 通配符。反代不会改变单进程容器结构：HTTPS 终止服务由用户已有站点基础设施承担，不内置到镜像。

## 4. 替代：只下载预编译前端 `dist/`

计划每个 Release 同时附上 `openlist-frontend-custom-<version>.tar.gz` 和 SHA256SUMS，包内顶层为 **`dist/`**。下载对应真实版本，核验后解压到新的暂存目录，检查 `dist/index.html`、`dist/assets/`、`dist/static/` 完整存在。

已有官方 OpenList 用户可备份后单独使用静态包，例如宿主机新目录 `/srv/my-openlist/dist` 挂载为 `/opt/openlist/data/dist`，`config.json` 的 `dist_dir` 设置为 `/opt/openlist/data/dist`，只重启指定实例。这里是路径示例，不代表你当前挂载；先查 `docker inspect`，不要照抄到生产目录。

**静态前端不包含 Go 后端**：原版官方后端没有本项目原生 `/icon/` 功能，只复制 `dist/` 不会实现素材上传、删除和目录接口。需要完整功能则使用集成镜像；旧 sidecar 路径/数据不能未经验证直接搬到新 `data/icon/`。

## 5. 本地构建与隔离验证

```sh
# 项目根目录，不会替换已有生产容器
sh -n scripts/install.sh
docker build --build-arg VERSION=custom-local -t openlist-custom:custom-local .
docker run --rm --network none --read-only openlist-custom:custom-local version
```

另用全新的临时目录、名称和仅回环端口做 HTTP/鉴权/重启验收。禁止将验证命令指向生产数据库或在没有合法登录时伪造管理员。真实 image build 和管理员验收必须实际成功后才标记通过；写好 Dockerfile 不代表镜像已构建或发布。

## 6. 数据、更新与回滚

容器内 `/opt/openlist/data` 对应专属 bind 目录，包含 OpenList 配置/数据库及私有 **`icon/`** 素材数据。不要公开数据库/原图/回收数据，不只备份前端；删除保留数据计入配额，不自动清空。

- **升级前**：记录固定镜像版本、`docker inspect` 挂载/端口/环境；停止自己目标实例后完整复制专属 data 到新的备份目录，保存目录权限。先在备份副本和不同端口验证新镜像。
- **升级时**：显式选择已验证版本。安装脚本只做首次安装，拒绝已有目录，不用于原地升级；不得把新镜像的空配置复制覆盖业务数据。
- **回滚时**：保留旧固定镜像和完整对应数据备份。新旧数据库若有迁移不能只换旧镜像；恢复数据副本之前确认容器完全停止并记录范围。
- **已有旧 `/icon-library/` 选择/sidecar 数据**：属于单独迁移工作，不能宣称自动兼容。保留旧配置与原始数据，审核标识符/预览引用/隐藏项后隔离验证，再授权生产切换。

历史资料：`docs/install.md`、`docs/icon-service.md`、`docs/simple-deployment-plan.md`、`docs/two-container-plan.md` 及 `deploy/icon-service/` 仅为旧部署回溯保留，不是当前安装方案。
