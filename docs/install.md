# 安装官方 OpenList 后切换二改前端

本指南面向已安装的 **OpenList v4.2.6 Docker**；不改官方镜像、不替换数据库、不复制测试账号。现有容器名、宿主机挂载路径和端口以你的实际配置为准。

## 1. 查清现有挂载

```sh
docker inspect openlist --format '{{json .Mounts}}'
```

以下例子假定宿主机 `/srv/openlist/data` 挂到容器 `/opt/openlist/data`。不是要求改成这个路径；已有站点请替换例子路径，不要创建第二份数据库。

## 2. 获取完整构建产物

按仓库 README 构建，或从 Actions 下载 `openlist-frontend-custom` artifact，解压得到 `openlist-frontend-custom.tar.gz` 和 `SHA256SUMS`。先在下载目录核对：

```sh
sha256sum -c SHA256SUMS
mkdir -p extracted-dist
tar -xzf openlist-frontend-custom.tar.gz -C extracted-dist
test -f extracted-dist/index.html
test -d extracted-dist/assets
test -d extracted-dist/static
```

本地构建直接使用仓库 `dist/`，不需要上述解压。不要只复制 `index.html` 或只复制 `assets`。

## 3. 备份并复制

下面命令在维护窗口执行，需要能管理 Docker 和数据目录。停止目标容器以保证数据库备份一致；有任务时应先等任务结束。

```sh
DATA=/srv/openlist/data
BUILD=/path/to/extracted-dist
STAMP=$(date +%Y%m%d-%H%M%S)
BACKUP="$DATA/_recovery/frontend-$STAMP"
mkdir -p "$BACKUP"
docker stop openlist
cp -a "$DATA/config.json" "$BACKUP/config.json"
cp -a "$DATA/data.db" "$BACKUP/data.db"
# 如果已有 custom-dist，保留原部署：
if [ -d "$DATA/custom-dist" ]; then
  cp -a "$DATA/custom-dist" "$BACKUP/custom-dist"
fi
mkdir -p "$DATA/custom-dist/assets"
# 先复制哈希资源，后更新入口；不删除旧资源，照顾已打开的页面。
cp -a "$BUILD/assets/." "$DATA/custom-dist/assets/"
cp -a "$BUILD/." "$DATA/custom-dist/"
```

非 SQLite 后端应按自己的数据库类型另行备份，不能套用 `data.db` 命令。

## 4. 只修改 dist_dir

编辑已有 `$DATA/config.json`，只把 `dist_dir` 改为容器内路径：

```json
"dist_dir": "/opt/openlist/data/custom-dist"
```

**JSON 中这行只是字段片段，不是整份配置。** 保留 JWT、数据库、存储、端口等其它值；不要把仓库样例覆盖整份配置。若 `cdn` 指向其它前端地址，需理解其用途并移除前端 CDN 干扰后再验收，不要盲目改业务代理。

```sh
docker start openlist
```

OpenList 会缓存 HTML 模板，所以已经运行时仅复制文件通常不够，需要重启目标容器。不要重启其它实例。

二改毛玻璃默认值来自前端；已有站点的毛玻璃、图标设置仍由本实例后台 `customize_head` 保存。没有复制另一实例的这些字段，就不会自动套用另一实例的图标配色/选择。

## 5. 接入图标素材服务

必须继续阅读 [图标服务对接](icon-service.md)。素材接口不是官方 OpenList 自带接口，未接入 `/icon-library/api/catalog` 时面板会报不可用。推荐独立图标容器，官方 OpenList 容器保持不变。

所有读写都要在站点同源 HTTPS 下访问，尤其图标保存使用 Web Locks；直接 HTTP IP/端口可能缺少安全上下文，不可作为完整管理验收。

## 6. 验收清单

先用你的站点域名替换 `https://openlist.example`：

```sh
curl -f https://openlist.example/ping
curl -f https://openlist.example/api/public/settings
curl -f https://openlist.example/api/public/archive_extensions
curl -f https://openlist.example/icon-library/api/catalog
```

注意 OpenList 的业务 API 可能 HTTP 200 但 JSON `code` 不为 200，不能仅以 curl 成功判定。素材 catalog 必须 JSON、`code:200`，不能是带 `Loading...` 的 HTML。

浏览器进一步确认：
- HTML 入口引用的 `/assets/index-*.js` 是二改构建，新开无缓存页面能显示目录。
- 实际 `.js`、`.css`、WASM 请求不是 SPA HTML 200；本地 `static` 文件可加载。
- 手机/桌面首页、子目录、搜索、预览、返回定位正常。
- 合法管理员打开 `/@manage/icons`，刷新素材目录、上传 PNG、选择并保存、重新加载确认。
- 删除正在使用素材应拒绝；用明确的测试上传素材验收删除，不删除业务素材。
- 已存在壁纸/计数/公告内容依赖实例设置和外站，不认为前端构建会自动迁移这些内容。

## 7. 回滚

```sh
docker stop openlist
cp -a "$BACKUP/config.json" "$DATA/config.json"
# 如果此前已使用 custom-dist，可将其恢复；整目录恢复前先另外保留当前副本。
# 如果此前 dist_dir 为空，恢复配置后会重新使用镜像内置官方前端。
docker start openlist
```

仅前端回滚不需要覆盖数据库。只有确有数据库故障、理解数据回退后果时才恢复数据库备份。

## 本项目整理时的现有实例

以下只是实际部署关系，便于避免混淆，不是通用安装要求：
- 开发源码 `/openlist/frontend`，部署 `/openlist/dist`，使用 `openlist-dev`。
- 正式副本 `/www/wwwroot/docker/openlist/custom-dist`，容器 `openlist` 的 `dist_dir=/opt/openlist/data/custom-dist`。
- 二者独立；提交仓库、构建新包不会自动更新任一生产实例。
