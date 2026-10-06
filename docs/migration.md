# 配置迁移、备份与恢复

## 不属于前端文件的东西

相同前端、相同素材目录不等于相同显示结果。OpenList 的显示/功能设置在自己的数据库中，浏览器偏好则按 origin 分开。

迁移另一实例时应明确选择：
- `customize_head` 的 `OPENLIST-HOME-GLASS-START/END`：透明度、壁纸。
- `customize_head` 的 `OPENLIST-ICON-THEME-START/END`：各类/后缀使用哪个图标。素材服务复制不会自动复制这些选择。
- `customize_body`：公告卡片、计数图片、一言、运行时间及其自定义 CSS/脚本。导出时检查是否含公开不合适的信息或访问密钥。
- 分页、图片类型、iframe 预览等显示功能设置：按需要逐项处理，不全量覆盖另一实例。

**不要为了同步界面而复制用户、权限、JWT、存储账号或整份测试数据库。** 推荐用你自己的合法管理员登录，在目标实例后台逐项迁移。原始 head 编辑器有托管块隔离，图标/毛玻璃配置优先用各自管理入口保存。

图标引用形如 `upload-<32hex>`；迁移已有选择时必须同步该素材 catalog、预览、原始文件与 tombstone/trash，不能仅复制可见 WebP。保持已有 ID 不变。缺失预览会回退原生图标，但这不算迁移完成。

浏览器 `localStorage` 的布局、排序、编辑器字号、播放偏好、阅读进度按域名单独保存。不要从浏览器导出所有 storage 后整体导入：其中可能含登录 token 或密码。需要同步偏好时只对明确非敏感键处理。

## Docker 素材服务恢复范围

独立容器方案中备份：
1. OpenList 数据库/配置/存储相关文件，及完整 `custom-dist/`。
2. 图标服务整个私有数据目录（catalog + originals + previews + trash），服务停止后复制或使用一致性备份。
3. Compose、经过保密检查的 `.env`、图标镜像（或可重建的源码、Dockerfile、锁定依赖）。
4. 网站 Nginx 反代配置与 TLS 证书。独立图标容器消除了宿主机 Python/systemd 依赖，**没有自动容器化你的现有网站入口**。

绑定挂载的数据不在 `docker save` 的镜像中。导出镜像不等于备份数据库，目录备份也不保证镜像能下载。需要离线恢复时分别保存两者。

```sh
# IMAGE 为实际构建图标镜像名；不要误导出另一实例。
docker save -o openlist-icons-image.tar IMAGE
sha256sum openlist-icons-image.tar > openlist-icons-image.tar.sha256
# 新机：
sha256sum -c openlist-icons-image.tar.sha256
docker load -i openlist-icons-image.tar
```

UID/GID、数据目录 0700 权限与容器内低权限用户一致；复制时保留所有权或依据部署说明恢复。不把 originals/trash/数据库配置成 Nginx alias。

## 原有宿主机服务切到 Docker 的步骤边界

仓库的独立 Docker 示例是可选择方案，并不自动操作现有站点。切换必须先确认端口、维护窗口、回滚路径：
- 停止原素材服务，完整备份私有数据，再让新容器挂载同一份或独立拷贝数据；不能两个进程同时操作同一 catalog。
- 确认 Docker 服务目录和所有预览可读，未登录/错误 Origin 的写请求被拒绝。
- Nginx 指向新的 loopback 端口，`nginx -t` 成功后 reload。
- 用合法管理员完成上传/删除与保存验收后，再禁用旧服务开机启动；旧数据/配置保留用于回滚。
- 失败时先停新容器再恢复旧服务/代理，不同时运行两个写入方。

## 使用者验收与安全限制

- 图片处理接受实际静态 PNG，1 字节～5MiB，限制尺寸/像素/APNG；转为独立 immutable WebP 预览。
- 权限必须来自目标 OpenList 真实 `/api/me`，启用管理员 role2；同 token 读取 head 做删除引用保护。
- 页面 Web Locks 不是跨浏览器数据库原子锁；素材删除和 OpenList 设置保存仍有跨服务竞态，不能宣称完全消除。
- immutable 预览删除后可能在已有浏览器缓存中保留；源服务后续请求会拒绝，不是撤回已下载的图片。
- 回收 trash 保留用于恢复，并计入容量上限；没有默认自动清空操作。
- 没有真实管理员凭据时，只能验收公共读取与拒绝探针，不能伪造登录宣称上传/删除成功。
