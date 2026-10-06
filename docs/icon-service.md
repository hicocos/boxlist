# PNG 图标 API 与独立 Docker 接入

> 这是部署示例，不会自动修改现有 OpenList、nginx、证书或业务数据库。只有自定义前端才有图标管理入口；官方前端不会因启动这个容器自动出现新页面。后端仍使用官方 OpenList。

## 1. 结构与前提

```text
浏览器 https://openlist.example
  └─ 原有站点 nginx（HTTPS / 证书仍单独管理）
      ├─ /、/api/… → 原有 OpenList 与自定义静态前端
      └─ /icon-library/… → 127.0.0.1:5235 独立 icons 容器
                                 └─ 只读鉴权/设置 → 127.0.0.1:5244 原有 OpenList
```

- 镜像是 Python 3.13 slim + Pillow 12.3.0，仅运行 `python -m icon_service`，固定绑定 `127.0.0.1:5235`；没有 `--port` 参数或对外监听开关。
- Compose 使用 **Linux 的 `network_mode: host`**，所以容器内的 loopback 是宿主机 loopback，既能被宿主机 nginx 访问，也能访问现有 OpenList 的宿主机映射端口。不要加 `ports:`，不要换成普通 bridge 后以为容器 loopback 是宿主机。
- 本示例假定 nginx 在宿主机；若 nginx 在 bridge 容器内，它的 `127.0.0.1` 不是宿主机，不能照搬代理配置。Docker Desktop、rootless/userns 映射、SELinux 环境需要另行适配并验证，不能宣称与本示例完全等价。
- 先检查 `ss -lnt '( sport = :5235 )'`。若已占用，不停止现有服务，也不启动第二个实例；本版本固定端口，同宿主机不能并行启动多个实例。
- `OPENLIST_ICONS_BACKEND` 必须是显式 loopback IP 的 HTTP origin，如 `http://127.0.0.1:5244`，替换成**本次站点后端**的真实宿主机端口；不接受 `localhost`、远端域名、Docker 服务名、凭据、额外路径。不要把生产鉴权指向另一套开发数据库。
- `OPENLIST_ICONS_ORIGIN` 必须是浏览器实际使用的完整 HTTPS Origin，**无尾斜杠、无路径**；非默认端口需要保留，如 `https://openlist.example:8443`。它不是后端地址，域名别名也不会自动被允许。

## 2. 准备配置并启动（部署者明确选择后执行）

从仓库根目录进入示例目录；路径仅为通用示例：

```sh
cd deploy/icon-service
cp .env.example .env
# 编辑 .env：真实的 OpenList loopback 端口、HTTPS Origin、专用数据路径。
# 此目录与数据目录均应位于公网静态目录之外。
sudo install -d -m 0700 -o 999 -g 989 /srv/openlist-icons/uploads
docker compose --env-file .env -p openlist-icons config
docker compose --env-file .env -p openlist-icons up -d --build
docker compose --env-file .env -p openlist-icons ps
curl --fail http://127.0.0.1:5235/icon-library/api/health
curl --fail http://127.0.0.1:5235/icon-library/api/catalog
```

将上面的目录与 `.env` 的 `OPENLIST_ICONS_DATA` 保持一致。不要对已有业务目录运行递归 chown，不要将 OpenList 自身数据库挂载进来。Compose 禁止自动创建不存在的 bind 源目录，避免 Docker 建出 root 所有目录后服务启动失败。

环境配置：

| 变量 | 示例 / 意义 |
| --- | --- |
| `OPENLIST_ICONS_IMAGE` | `openlist-icons:local`，本地构建镜像标签 |
| `OPENLIST_ICONS_DATA` | `/srv/openlist-icons/uploads`，宿主机独立私有绝对路径 |
| `OPENLIST_ICONS_ROOT` | `/var/lib/openlist-icons`，容器内同一 bind 的挂载点与服务数据根 |
| `OPENLIST_ICONS_BACKEND` | `http://127.0.0.1:5244`，已有后端宿主机端口 |
| `OPENLIST_ICONS_ORIGIN` | `https://openlist.example`，实际站点 Origin |

安全配置是固定非 root `999:989`、只读根文件系统、`cap_drop: ALL`、`no-new-privileges`、16 MiB 受限 `/tmp` tmpfs、连接/内存/PID 限制；数据目录 0700，数据库与素材 0600。所有业务写入仅进入独立 bind。镜像不包含测试、数据库、uploads、`.env` 或生产凭据，且没有认证绕过环境变量。

`health` 仅证明服务能响应 HTTP，不证明 OpenList 后端或管理员鉴权正常。公开 catalog 列出有效上传素材的名称等非秘密元数据；不要上传需要保密的名称或图像。

## 3. nginx：整个命名空间代理，不得回落 HTML

参考 [`deploy/icon-service/nginx.conf.example`](../deploy/icon-service/nginx.conf.example)，把内容放在**原站点 HTTPS server 块内部**，不要创建另一套不相关的站点。实际安装时先备份原配置，在部署者确认后 `nginx -t`，通过再 reload；仓库示例不会自动执行。

关键规则：

- `location ^~ /icon-library/` 覆盖 API、预览和错误路径，防止其他正则 location 接管。
- `proxy_pass http://127.0.0.1:5235;` **不加尾斜杠/URI 后缀**，保持服务要求的完整路径。
- `Authorization $http_authorization` 与 `Origin $http_origin` 原样转发，不添加 `Bearer`，不替换成固定 token，不把 Origin 写死成“允许来源”。客户端没有 Origin 时也不得替它伪造。
- 不对这个 location 设置 `try_files … /index.html`、文件系统 alias 或 API 缓存。不公开 originals、trash、SQLite/WAL/SHM 或 staging。
- `proxy_intercept_errors off` 保留服务自身 JSON 与 HTTP 状态；示例另设局部 error_page，覆盖上层可能继承的 SPA 错误页。**nginx 自身拒绝/代理故障统一返回 JSON 503**（包含 nginx 的体积限制错误），不是伪装为 HTML 200；上游服务返回的 401/403/404/409/413 等状态仍原样保留。
- 上传使用 raw body + Content-Length，不是 multipart；默认缓冲避免向服务转成 chunked。本示例限制 nginx body 为 5m，不应在 CDN/WAF 上额外缓存写入或 JSON API。

接入后验证真实 HTTPS：

```sh
curl -i https://openlist.example/icon-library/api/health
curl -i https://openlist.example/icon-library/api/catalog
curl -i https://openlist.example/icon-library/not-found
```

前两项应为 JSON 200，最后一项应为 JSON 404，不能是 OpenList 的 index.html。只有浏览器登录真实管理员后才可验收写入；不要把上述公开 GET 成功当成鉴权测试。

## 4. 前端接入与使用

自定义前端图标管理路由是 `/@manage/icons`（由原有管理员身份控制）。前端已经使用同源 `/icon-library/api`，正常根路径部署**不需要配置远端 API 地址、CORS 或新 token**。管理员登录沿用 OpenList 原生登录与 Authorization；服务向现有后端的 `/api/me` 验证整数 `role=2`、`disabled=false` 与正整数用户 ID。

1. 先安装自定义静态前端，并保持原有后端与用户/存储配置；启动 sidecar 不等于完成前端切换。静态前端切换见仓库主安装文档。
2. 图标管理页可选择内置图标、上传 PNG 或回到原生默认图标。图标选择保存在原生 `customize_head` 的非执行、百分号编码 META `OPENLIST-ICON-THEME` 块内，不使用未注册后端设置键。
3. 上传成功只代表素材进入图库，**不会自动改当前类型图标**；选择图标后还需在管理页保存配置。上传后不用重新编译前端。
4. 删除前先将使用该素材的类型改为其他图标并保存。页面草稿/基线与服务最新保存配置都参与保护；服务不能读取另一浏览器尚未保存的草稿。
5. 遇到上传超时/断网，保留原文件和原 `X-Upload-Id` 重试并读回 catalog；不要每次换 key，也不要为了“补偿”不确定结果自动删除。离开页面/重载会丢失仅在页面内保存的未完成上传任务。
6. 子路径部署不是本示例已验证范围：前端可能带 base path，而服务只接受根 `/icon-library/…`；需单独设计代理去前缀并验证，不能直接宣称兼容。

## 5. API 契约

JSON API/错误均 `application/json; charset=utf-8`、`Cache-Control: no-store`、`nosniff`。严格匹配路径，不支持查询参数变体、目录列举或 HTML fallback；不返回 CORS 放行头。

| 方法与路径 | 权限 / 作用 |
| --- | --- |
| `GET /icon-library/api/health` | 公开，`{"code":200,"data":{"version":1}}` |
| `GET /icon-library/api/catalog` | 公开，version、active `assets`、`hidden_builtins` |
| `GET/HEAD /icon-library/assets/upload-<32位小写hex>.webp` | 公开有效预览；不存在/删除为 404 |
| `POST /icon-library/api/upload` | 精确 Origin + 原生管理员 Authorization；一个 raw PNG |
| `DELETE /icon-library/api/assets/<id>` | 同样鉴权，无请求体；上传 ID 或内置白名单 ID |

上传头：

```text
Origin: https://openlist.example
Authorization: <当前登录的原生 OpenList Authorization，原样传递>
Content-Type: application/octet-stream
Content-Length: <实际文件字节数>
X-Icon-Name: <encodeURIComponent(filename.png)>
X-Upload-Id: <客户端密码学随机生成的32位小写hex>
```

不要将真实 Authorization 写入 `.env`、仓库、截图或日志，也不要从服务端数据库提取 token。前端正常登录会按原生方式携带请求。

上传返回 `data.asset`（id/name/width/height/bytes/created_at）和 `data.reused`。同管理员、同 key、同字节幂等；同 key 不同字节 409；不同 key 即使文件相同也保留独立素材。删除后原 key 返回 410，不复活旧 immutable ID。公开名称必须按文本渲染，禁止 innerHTML。

图片限制：**1 字节至 5 MiB**（不是最小 1 MiB），`.png` 扩展名、PNG 签名、完整容器/CRC、Pillow verify 与实际 load；单边 ≤4096、总像素 ≤16,000,000。拒绝改名 JPEG/WebP/GIF/ICO/SVG、APNG（含单帧动画控制块）、截断、追加垃圾与损坏图片。等比缩至 232×232 范围、不放大小图，居中于透明 256×256，转换成无损 WebP 并剥离公开预览元数据；原 PNG 私有保留。

## 6. 删除、缓存与并发边界

- 服务两次用同 token 只读核对 `/api/admin/setting/get?key=customize_head`；存在引用返回 409“正在使用，请先更换并保存”。保守保护旧 `OPENLIST-FOLDER-ICON` 的 smile；配置异常、重复 META/JSON 键、未知格式或后端不可用**拒绝删除**，不当作空配置。
- 内置删除只是写 `hidden_builtins` tombstone，不删除编译资源；上传删除写持久 tombstone，原 PNG/WebP 移到私有 recoverable trash，catalog 不再列出、源站预览返回 404。没有公开恢复/清空 trash API。
- 上限 **200 个上传项、累计 256 MiB 原图**包含已删除的保留 trash；删除不会释放配额，内置隐藏不占上传项配额。达到上限需明确授权的离线维护/归档策略，不能直接删 SQLite 行或 trash 当作正常操作。
- WebP 返回 `public, max-age=31536000, immutable`；ID 不变时不会覆盖字节。删除不能撤销浏览器/CDN已缓存或已开始读取的公开图像。验证删除应禁用缓存访问原 URL，**不要加查询串**（严格路由会令任何查询串返回 404，产生假验证）。
- 两次后端读取 + 本地 SQLite 删除不是跨 API 原子事务。其他浏览器/外部 API 仍可能在最后检查后保存旧 ID；前端保存前重新验证图库、同浏览器 Web Lock、冲突处理只能缩小竞态，不能保证全局原子性。不存在素材应回退原生图标。

## 7. Docker-only 服务迁移边界

此方案把**图标服务进程**独立放进 Docker，不在 OpenList 容器启动第二个进程，也不需要改其 entrypoint。宿主机 nginx 和证书仍由原站点单独维护；这不是“全站全部进 Docker”的方案。

若以后明确批准从旧 systemd 图标服务迁移：先做完整一致性备份，停止旧 writer 后冷复制整套数据（包括 catalog、所有原图/预览、tombstone、trash；不要只复制 WebP），校验，再为新专用目录设 UID/GID 999:989。SQLite 运行时不能随意只复制 `.db` 忽略 WAL；停服冷备或 SQLite backup 后仍需保持文件集合一致。保留原备份和旧服务定义。确认 5235 空闲、新容器配置正确后启动并核对 catalog 与真实管理员流程，最后在确认后调整原 nginx。旧服务若使用其他端口应同步核对，不能默认两套目录/数据会自动同步。

本仓库示例未替你迁移任何实例，不读取/写入真实数据库，不改系统用户、不停旧服务、不 reload 实际 nginx。

## 8. 测试与已知限制

在仓库根目录运行 Docker smoke：

```sh
OPENLIST_ICON_TEST_SCRATCH=/your/private/test-scratch python3 deploy/icon-service/smoke-test.py
```

该脚本适用于 **rootful Linux Docker，执行用户须能为 scratch bind 设 UID999/GID989**。复制最小上下文到独立 scratch 后构建 `openlist-icons-repo-test:local`，用 `--network none` 的临时容器运行真实 CLI，fixture 后端在同一隔离网络内。实测 health/catalog、PNG 上传与幂等、管理员/Origin 拒绝、实际 WebP 解码、已用删除保护、私有 trash、重启与硬化。清理临时容器，保留 scratch `report.json` 和 `commands.log`，无宿主机端口映射。**部署用 host-network Compose 仅解析验证，不自动启动**，避免抢占真实 5235。

更多单元/进程测试见 [`icon-service/README.md`](../icon-service/README.md)。若本机安装 nginx，可额外运行 `OPENLIST_ICON_TEST_SCRATCH=/your/private/test-scratch python3 deploy/icon-service/test-nginx.py`；它在临时端口和独立 scratch 配置中验证片段、原样请求头、上游 JSON 404、代理故障与超限 body 均无 HTML fallback，并只停止自己的临时进程。某些 OpenResty/面板 nginx 需要额外 `NGINX_TEST_LUA_PATH='/path/to/nginx/lib/lua/?.lua;;'`，该设置只写入临时测试配置，不改真实站点。默认不访问真实 OpenList；只读 live 探针显式 opt-in。fixture 通过不等于真实管理员验收、公开 HTTPS/证书验收或真实移动端验证。基础镜像 `python:3.13-slim` 是浮动标签，Pillow 固定 12.3.0；严格复现应在自行审核后锁定镜像 digest，并定期重新构建获取安全更新。
