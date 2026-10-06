# OpenList 二改前端

基于 [OpenListTeam/OpenList-Frontend](https://github.com/OpenListTeam/OpenList-Frontend) 的 **v4.2.6** 前端，保存当前本地二改源码与可复现构建方法。

**范围必须区分：**
- 官方 OpenList 后端源码和二进制不在这里修改。
- 前端界面、交互、图片/视频预览等二改在 `src/`。
- 图标素材上传/删除是**额外的自定义后端**，源码在 `icon-service/`，不是仅靠前端实现。可独立容器部署，绝不混装进官方 OpenList 容器。
- 仓库不是站点数据库备份：不包含登录凭据、存储账号、生产配置、上传素材或浏览器阅读进度。

## 包含的二改

- 毛玻璃/透明度与壁纸配置，保留原生滚动与下拉刷新行为。
- 文件类型图标管理、内置素材、自定义 PNG 素材库、冲突合并与保存保护。
- 紧凑搜索、根目录请求、请求取消/超时保护和结果保留。
- 长文件名/路径、多种布局、目录返回定位和选中交互修复。
- 图片轻量预览、目录画廊、连续/单张目录阅读器及进度恢复。
- 手机视频控制布局、播放时间与画中画；全屏保留原版布局。
- 分组本地设置和移动端工具栏改进。

详细行为以源码与测试为准。管理员真实上传/删除验收必须使用自己的合法登录，不提供鉴权绕过。

## 安装入口

1. 先安装官方 OpenList，保留自己的数据库和存储配置。
2. 按 [安装与切换前端](docs/install.md) 构建/下载 `dist`，放进容器可见目录，设置 `dist_dir`，重启目标容器。
3. 按 [图标素材接口对接](docs/icon-service.md) 部署独立图标服务并代理同源 `/icon-library/`。
4. 按 [配置迁移与备份](docs/migration.md) 迁移需要的显示配置；不要覆盖账号、存储、权限。

**只切换前端不等于完整迁移素材库。** 图标面板依赖独立服务的目录接口，未接入会提示“图标素材服务不可用”。已有图标选择还需要保留 `customize_head` 中的配置块。

## 本地构建

需要 Git、Node.js **24.21.0**、pnpm **11.24.0**。使用现有锁文件，不要无故升级依赖。

```sh
git clone https://github.com/hicocos/openlist-frontend-custom.git
cd openlist-frontend-custom
npm install -g pnpm@11.24.0
pnpm install --frozen-lockfile
pnpm run test:ux
pnpm run lint
VITE_API_URL=/ pnpm run build
```

`VITE_API_URL=/` 很重要：使用当前站点同源 API，不要写测试域名或忘记变量。`dist/` 包括 `assets/` 和 `static/`，部署必须整体复制，不能只拿入口 JS。

仓库已包含中文与英文语言文件，普通构建不需要 Crowdin 凭据。保留上游 `build.sh` 作为参考，推荐直接用上述命令，避免其修改版本/下载语言流程。

## GitHub 构建

Actions 的 `Validate and build` 工作流运行测试、类型检查和构建，并上传带校验文件的 `openlist-frontend-custom` artifact。私有仓库下载 artifact 需要登录。也可以完全本地构建。

## 仓库结构

- `src/`, `public/`, `patches/`, `tests/`：前端源码、资源、依赖补丁、交互测试。
- `icon-service/`：独立素材后端源码与隔离测试。
- `deploy/icon-service/`：独立 Docker/反代部署例子。
- `docs/`：安装、对接、迁移与验收说明。

## 兼容性与限制

当前基线是官方 OpenList **v4.2.6**。其它版本需要重新验证接口、页面注入和权限行为。不是承诺以后任意 `latest` 都兼容。

本地预览依赖（编辑器、公式、流程图、ASS 字幕等）随完整构建输出；部分 Office/Flash/EPUB 服务及背景、计数图片仍使用外部 CDN/用户配置地址，仓库不承诺完全离线。

所有实际部署例子先阅读再执行；仓库发布不自动改动生产站点。框架测试、接口探针不替代真实管理员上传/删除或真机手势验收。

## 来源与授权

前端上游基线 commit：`0725e589bb7f0e734a7446f427eed2175f26c15c`。保留上游 MIT [LICENSE](LICENSE)。第三方依赖按各自许可证使用。现有图标图片的作者/授权范围未逐一核实；仓库默认私有，公开或再分发前应单独确认素材授权，不把代码的 MIT 声明视为所有图片均已获授权。
