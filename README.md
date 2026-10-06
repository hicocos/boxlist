<div align="center">

# boxlist

支持多种存储的文件列表程序，提供文件管理、在线预览和个性化界面。

[安装](#安装) · [问题反馈](https://github.com/hicocos/boxlist/issues)

</div>

---

本项目基于 [OpenList](https://github.com/OpenListTeam/OpenList) 开发。

## 功能

- [x] 多种存储
  - 本地存储
  - 阿里云盘、百度网盘、天翼云盘、123 云盘、115、夸克、UC、迅雷、蓝奏云
  - OneDrive / SharePoint、Google Drive、Dropbox、PikPak、MEGA
  - S3、Azure Blob Storage、又拍云
  - WebDAV、FTP、SFTP、SMB
  - Seafile、Cloudreve
- [x] 文件上传、下载、重命名、移动、复制与删除
- [x] 文件搜索、列表与网格布局
- [x] 文件与文件夹打包下载
- [x] 跨存储复制文件
- [x] 图片预览与目录画廊
- [x] 连续阅读、单张阅读与阅读进度记忆
- [x] 音视频播放、歌词、字幕与画中画
- [x] PDF、Markdown、代码、文本及 Office 文档预览
- [x] `README.md` 渲染
- [x] 离线下载
- [x] WebDAV 服务
- [x] 密码保护与用户权限管理
- [x] 深色模式与多语言
- [x] 毛玻璃效果、透明度与自定义壁纸
- [x] 文件类型图标、自定义图标上传与素材管理
- [x] 目录返回定位与阅读进度恢复
- [x] 移动端适配
- [x] Docker 部署

## 安装

```sh
git clone https://github.com/hicocos/boxlist.git
cd boxlist
docker compose up -d --build
```

访问 `http://服务器IP:5244`。初始管理员信息可通过以下命令查看：

```sh
docker compose logs boxlist
```

## 反馈与贡献

通过 [Issues](https://github.com/hicocos/boxlist/issues) 提交问题或功能建议，欢迎提交 Pull Request。

## 许可证

后端采用 [AGPL-3.0](backend/LICENSE)，前端采用 [MIT](LICENSE)。第三方组件与素材遵循各自许可证。
