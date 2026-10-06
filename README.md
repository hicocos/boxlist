# boxlist

一个集文件管理、媒体预览与个性化界面于一体的自托管应用。前端、后端和图标素材服务集成在 **一个 Docker 容器** 中，无需另装图标服务。

> **后端扩展：** 内置 PNG 图标上传、素材列表、WebP 预览、图标删除与隐藏；支持管理员权限校验、在用素材删除保护和重启持久化。接口统一为 `/icon/`。更新时需要保留这些功能，具体见 [后端维护说明](docs/backend-changes.md)。

## 功能

- **文件管理**：多种文件布局、搜索、目录返回定位。
- **个性化界面**：毛玻璃效果、透明度、壁纸、自定义文件类型图标。
- **图标管理**：内置素材选择、PNG 上传、预览、删除与隐藏。
- **图片阅读**：目录画廊、连续阅读、单张阅读、进度记忆。
- **媒体预览**：图片、音视频及支持的文档格式预览。
- **移动端适配**：紧凑工具栏、视频时间显示、画中画与本地设置。

## 部署

**公共安装包和镜像尚未发布。** 发布后可从 Releases 下载固定版本，使用安装脚本部署；现在可在项目根目录自行构建：

```sh
docker build --build-arg VERSION=custom-local -t boxlist:local .

# 使用全新的数据目录，不要指向已有实例的数据。
sudo mkdir -m 0750 /srv/boxlist
sudo chown 1001:1001 /srv/boxlist

docker run -d --name boxlist --restart unless-stopped \
  -p 127.0.0.1:5246:5244 \
  --mount type=bind,src=/srv/boxlist,dst=/opt/openlist/data \
  boxlist:local
```

将自己的域名反向代理到 `http://127.0.0.1:5246`，配置 HTTPS；在数据目录的 `config.json` 中设置 `site_url` 为实际 HTTPS 地址，修改配置前停止该容器，修改后再启动。

首次管理员信息通过 `docker logs boxlist` 查看，登录后及时修改密码。图标管理位于 `/@manage/icons`，上传与删除需要 HTTPS 和管理员账号。

完整步骤见 [部署指南](docs/quickstart.md)。默认使用内嵌前端，无需手动复制 `dist/`。

## 数据与更新

- 完整备份 `/srv/boxlist`：包含配置、数据库和 `icon/` 图标数据。
- 升级前备份，在独立数据副本上验证后再切换版本。
- 不覆盖已有数据库，不对已有数据目录执行递归权限修改。
- 已保存引用的图标不能直接删除；删除保留数据仍计入素材库配额。
- 已有实例的配置、图标数据与旧接口需要单独迁移，不会自动转换。

## 文档

- [部署指南](docs/quickstart.md)
- [后端维护与升级](docs/backend-changes.md)
- [测试与验收](deploy/integrated/VALIDATION.md)

## 许可证

前端适用 [MIT](LICENSE)，后端适用 [AGPL-3.0](backend/LICENSE)。组合分发与网络使用应遵守相应许可证，第三方素材遵循各自授权。
