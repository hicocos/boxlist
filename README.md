<div align="center">

# BoxList

基于 OpenList 的多存储文件管理程序，支持在线预览、图片阅读和个性化界面。

[![构建状态](https://github.com/hicocos/boxlist/actions/workflows/publish-image.yml/badge.svg)](https://github.com/hicocos/boxlist/actions/workflows/publish-image.yml)

[快速安装](#安装) · [使用文档](DEPLOYMENT.md) · [镜像下载](https://github.com/hicocos/boxlist/pkgs/container/boxlist) · [问题反馈](https://github.com/hicocos/boxlist/issues)

</div>

## 功能

- [x] 多存储接入：本地存储、网盘、对象存储、WebDAV、FTP、SFTP、SMB
- [x] 文件管理：上传、下载、搜索、复制、移动、重命名、打包下载
- [x] 在线预览：视频、音频、PDF、Office、Markdown、代码与文本
- [x] 图片阅读：画廊、连续阅读、单张阅读、阅读进度记忆
- [x] 预览图：批量生成、任务管理、自动规则、本地或 S3 存储
- [x] 个性化界面：壁纸、透明度、自定义 PNG / GIF 图标
- [x] 深浅色主题、多语言、移动端适配
- [x] 用户权限、目录密码、文件分享、WebDAV 服务
- [x] 一键安装、Docker 部署，支持 AMD64 / ARM64

## 安装

```bash
curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/install.sh -o install-boxlist.sh && sudo bash install-boxlist.sh install --install-docker
```

进入菜单后选择 `1` 安装，端口可自行设置（默认 `5244`）。安装完成会直接显示访问地址和初始账号密码。

运行 `sudo boxlist` 打开管理菜单，或使用 `sudo boxlist update` 更新。

其他部署方式见[部署指南](DEPLOYMENT.md)。

## 文档

- [安装与管理](DEPLOYMENT.md#一键安装与管理)
- [配置与反向代理](DEPLOYMENT.md#配置与网络)
- [备份与恢复](DEPLOYMENT.md#备份与恢复)
- [预览图使用说明](doc/thumb-operations.md)
- [S3 预览图存储](doc/derivative-s3.md)

## 反馈与贡献

欢迎通过 [Issues](https://github.com/hicocos/boxlist/issues) 反馈问题、提出建议，或提交 Pull Request 参与改进。

## 致谢

本项目基于 [OpenList](https://github.com/OpenListTeam/OpenList) 及其[前端](https://github.com/OpenListTeam/OpenList-Frontend)开发，感谢上游项目和所有贡献者。BoxList 是独立衍生项目，与 OpenList 官方无隶属关系。

## 许可证

前端采用 [MIT](LICENSE)，后端采用 [AGPL-3.0](backend/LICENSE)。第三方组件及素材遵循各自许可证。
