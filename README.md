# boxlist

自托管文件管理与媒体预览应用，支持个性化界面和图标管理。**一个 Docker 容器即可运行全部功能。**

> **后端扩展：** 内置 PNG 图标上传、素材列表、WebP 预览、删除与隐藏；支持管理员权限校验、在用素材删除保护和持久化。接口统一为 `/icon/`。更新兼容说明见 [后端维护](docs/backend-changes.md)。

## 功能

- 文件浏览、多种布局、搜索与目录返回定位。
- 毛玻璃效果、透明度、壁纸、自定义文件类型图标。
- 内置图标选择、PNG 上传、预览与素材管理。
- 图片画廊、连续/单张阅读、进度记忆。
- 音视频及支持的文档格式预览。
- 移动端工具栏、画中画与本地设置。

## 快速部署

需要 Git、Docker 和 Docker Compose。**公共镜像尚未发布，当前从源码构建：**

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose up -d --build
```

打开 `http://服务器IP:5244`，查看初始管理员信息：

```sh
docker compose logs boxlist
```

登录后及时修改密码。数据由 Docker 自动保存在持久化数据卷中，**无需手动创建目录或设置权限**。

### 配置域名与 HTTPS

图标上传与删除需要 HTTPS。将自己的域名解析到服务器，通过反向代理配置证书，再把整站代理到 `http://127.0.0.1:5244`。

在项目目录新建 `.env`，填写自己的地址：

```dotenv
SITE_URL=https://files.example.com
BIND_ADDRESS=127.0.0.1
```

执行 `docker compose up -d` 应用配置，从 HTTPS 域名访问 `/@manage/icons` 管理图标。示例域名请替换为自己的域名。

端口被占用时，在 `.env` 添加 `PORT=5246`，并同步调整访问地址和反向代理端口。默认 HTTP 端口对外开放，公开访问建议使用 HTTPS；详细配置见 [部署指南](docs/quickstart.md)。

## 数据与更新

- 重启或重建容器不影响数据卷中的配置、数据库和图标素材。
- **不要执行 `docker compose down -v`，它会删除数据卷。**
- 更新前备份数据，已有实例迁移见 [部署指南](docs/quickstart.md)。

## 文档

- [部署与备份](docs/quickstart.md)
- [后端维护与升级](docs/backend-changes.md)
- [测试与验收](deploy/integrated/VALIDATION.md)

## 许可证

前端适用 [MIT](LICENSE)，后端适用 [AGPL-3.0](backend/LICENSE)。组合分发与网络使用应遵守相应许可证，第三方素材遵循各自授权。
