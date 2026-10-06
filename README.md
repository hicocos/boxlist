# boxlist

自托管文件管理与媒体预览应用。

## 功能

- 文件浏览、多种布局、搜索与目录返回定位。
- 毛玻璃效果、透明度、壁纸与自定义文件类型图标。
- 图标上传、预览与素材管理。
- 图片画廊、连续阅读、单张阅读与进度记忆。
- 音视频及文档预览。
- 移动端适配与画中画。

## 部署

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose up -d --build
```

访问 `http://服务器IP:5244`。

查看初始管理员信息：

```sh
docker compose logs boxlist
```

## 文档

- [部署与备份](docs/quickstart.md)
- [维护说明](docs/backend-changes.md)

## 许可证

[MIT](LICENSE) · [AGPL-3.0](backend/LICENSE)
