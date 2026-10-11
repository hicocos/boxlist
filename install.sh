#!/usr/bin/env bash
# BoxList Docker installer and manager. SPDX-License-Identifier: MIT
# Menu layout adapted from OpenList-Resource/script/v4.sh (MIT),
# by ILoveScratch and the OpenList Dev Team.
# Copyright (c) 2025 The OpenList Projects Contributors
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
# THE SOFTWARE.
set -Eeuo pipefail

fail() { printf '错误：%s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法：bash install.sh [命令] [选项]
不带命令显示管理菜单。
命令：install update uninstall status password start stop restart backup restore
      docker schedule system about logs configure script-update
选项：
  --dir PATH          安装目录（默认 /opt/boxlist）
  --name NAME         新安装的项目及容器名（默认 boxlist）
  --port PORT         新安装的宿主端口（默认 5244）
  --bind ADDRESS      新安装的监听 IPv4 地址（默认 0.0.0.0）
  --site-url URL      新安装的对外 HTTPS 地址
  --image IMAGE      新安装的镜像（默认 ghcr.io/hicocos/boxlist:latest）
  --install-docker    缺少 Docker 时允许运行 Docker 官方安装脚本
  --existing-data MODE  残留数据卷处理：reuse 保留 / reset 删除重装
  --confirm-delete NAME 非交互删除确认，必须填写完整数据卷名
  --yes               确认彻底卸载（删除应用数据、安装目录及其中备份）
  --help              显示帮助
  --non-interactive   使用参数和默认值，不询问配置或进入管理菜单
更新前请备份。修改安装目录中的 .env 可调整镜像、端口和站点地址。
HELP
}

command_name=menu
install_dir=/opt/boxlist
instance=boxlist
port=5244
bind=0.0.0.0
site_url=
image=ghcr.io/hicocos/boxlist:latest
install_docker=false
yes=false
install_options=false
existing_data=
confirm_delete=
reuse_volume=false
reset_volume=false
non_interactive=false
port_explicit=false
bind_explicit=false
site_explicit=false
if [[ $# -gt 0 && $1 != -* ]]; then command_name=$1; shift; fi
while (( $# )); do
    case "$1" in
        --dir|--name|--port|--bind|--site-url|--image|--existing-data|--confirm-delete)
            (( $# >= 2 )) || fail "$1 缺少参数"
            case "$1" in
                --dir) install_dir=$2 ;;
                --name) instance=$2; install_options=true ;;
                --port) port=$2; port_explicit=true; install_options=true ;;
                --bind) bind=$2; bind_explicit=true; install_options=true ;;
                --site-url) site_url=$2; site_explicit=true; install_options=true ;;
                --image) image=$2; install_options=true ;;
                --existing-data) existing_data=$2; install_options=true ;;
                --confirm-delete) confirm_delete=$2; install_options=true ;;
            esac
            shift 2 ;;
        --install-docker) install_docker=true; shift ;;
        --yes) yes=true; shift ;;
        --help|-h) usage; exit 0 ;;
        --non-interactive) non_interactive=true; shift ;;
        *) fail "未知选项 $1；使用 --help 查看用法" ;;
    esac
done
case "$command_name" in
    menu|install|update|start|stop|restart|status|logs|password|configure|backup|restore|docker|schedule|system|about|shell|remove-container|script-update|uninstall) ;;
    *) fail "未知命令 $command_name" ;;
esac
[[ $(uname -s) == Linux ]] || fail '仅支持 Linux'
[[ $install_dir == /* && $install_dir != / && $install_dir != *$'\n'* ]] || fail '安装目录必须为非根目录的绝对路径'
install_dir=$(realpath -m -- "$install_dir")
[[ $install_dir != / ]] || fail '不能安装到根目录'
script_path=$(realpath -- "${BASH_SOURCE[0]}")

interactive=false
if [[ -t 0 && $non_interactive == false ]]; then interactive=true; fi
success='' warning='' reset=''
if [[ -t 1 && -z ${NO_COLOR:-} && ${TERM:-dumb} != dumb ]]; then
    success=$'\033[1;32m'; warning=$'\033[1;33m'
    reset=$'\033[0m'
fi
install_args=(--name "$instance" --image "$image")
[[ $port_explicit == false ]] || install_args+=(--port "$port")
[[ $bind_explicit == false ]] || install_args+=(--bind "$bind")
[[ $site_explicit == false ]] || install_args+=(--site-url "$site_url")
[[ $install_docker == false ]] || install_args+=(--install-docker)
[[ -z $existing_data ]] || install_args+=(--existing-data "$existing_data")
[[ -z $confirm_delete ]] || install_args+=(--confirm-delete "$confirm_delete")
heading() { printf '\n%s%s%s\n\n' "$success" "$1" "$reset"; }
pause_panel() { read -r -s -n1 -p '按任意键继续 ... ' _ || return 1; printf '\n'; }
clear_screen() { if [[ -t 1 && ${TERM:-dumb} != dumb ]]; then printf '\033[H\033[2J'; fi; }
if [[ $interactive == true && ${BOXLIST_MENU_CHILD:-0} != 1 ]]; then
    if [[ -t 1 && ${TERM:-dumb} != dumb ]]; then printf '\033[3J'; fi
    clear_screen
fi
valid_port() { [[ $1 =~ ^[1-9][0-9]{0,4}$ ]] && (( $1 <= 65535 )); }
valid_bind() {
    local octet
    local -a parts
    [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    IFS=. read -r -a parts <<< "$1"
    for octet in "${parts[@]}"; do
        if [[ ${#octet} -gt 3 ]] || (( 10#$octet > 255 )); then return 1; fi
    done
}
valid_site() { [[ -z $1 || $1 =~ ^https://[^[:space:]\'\"\$\`\\]+$ ]]; }
prompt_port() {
    local value
    while true; do
        read -r -p "访问端口 [$port]: " value || exit 0
        value=${value:-$port}
        if valid_port "$value"; then port=$value; return; fi
        printf '请输入 1–65535 之间的端口。\n'
    done
}
prompt_bind() {
    local value
    while true; do
        read -r -p "监听地址 [$bind，填 127.0.0.1 仅本机访问]: " value || exit 0
        value=${value:-$bind}
        if valid_bind "$value"; then bind=$value; return; fi
        printf '请输入有效 IPv4 地址。\n'
    done
}
prompt_site() {
    local value
    while true; do
        read -r -p "站点 HTTPS 地址 [${site_url:-留空稍后配置}，输入 - 清空]: " value || exit 0
        [[ $value != - ]] || value=__clear__
        value=${value:-$site_url}
        [[ $value != __clear__ ]] || value=
        if valid_site "$value"; then site_url=$value; return; fi
        printf '请输入 https:// 开头的地址，或留空。\n'
    done
}
panel_status() {
    local name state id
    if [[ ! -f $install_dir/.boxlist-managed ]]; then
        printf '  状态  %s未安装%s\n' "$warning" "$reset"; return
    fi
    IFS= read -r name < "$install_dir/.boxlist-managed"
    if ! command -v docker >/dev/null || ! docker info >/dev/null 2>&1; then
        printf '  状态  %sDocker 不可用%s\n' "$warning" "$reset"; return
    fi
    id=$(docker ps -aq --filter "label=com.docker.compose.project=$name" --filter 'label=com.docker.compose.service=boxlist' --filter 'label=com.docker.compose.oneoff=False')
    if [[ -z $id ]]; then
        printf '  状态  %s容器未创建 · 数据与配置保留%s\n' "$warning" "$reset"
    else
        state=$(docker inspect --format '{{.State.Status}}' "$id")
        case "$state" in running) state=运行中 ;; exited|created) state=已停止 ;; restarting) state=重启中 ;; esac
        printf '  状态  %s%s%s\n' "$success" "$state" "$reset"
        docker inspect --format '  镜像  {{.Config.Image}}{{range $port, $bindings := .HostConfig.PortBindings}}{{range $bindings}}{{printf "\n  端口  %s:%s" .HostIp .HostPort}}{{end}}{{end}}' "$id"
    fi
    printf '  实例  %s\n' "$name"
}
manage_panel() {
    local choice action had_install
    local -a args
    while true; do
        clear_screen
        printf '\n欢迎使用 BoxList 管理脚本 \n\n%s' "$success"
        cat <<'MENU'
基础功能：
1、安装 BoxList
2、更新 BoxList
3、卸载 BoxList
-------------------
服务管理：
4、查看状态
5、密码管理
6、启动 BoxList
7、停止 BoxList
8、重启 BoxList
-------------------
配置管理：
9、备份配置
10、恢复配置
-------------------
高级选项：
11、Docker 管理
12、定时更新
13、系统状态
14、关于
-------------------
0、退出脚本
MENU
        printf '%s\n' "$reset"
        read -r -p '请输入选项 [0-14]: ' choice || return 0
        case "$choice" in
            1) action=install ;; 2) action=update ;; 3) action=uninstall ;;
            4) action=status ;; 5) action=password ;; 6) action=start ;;
            7) action=stop ;; 8) action=restart ;; 9) action=backup ;;
            10) action=restore ;; 11) action=docker ;; 12) action=schedule ;;
            13) action=system ;; 14) action=about ;; 0) return 0 ;;
            *) printf '%s无效的选项%s\n' "$warning" "$reset"; pause_panel || return 0; continue ;;
        esac
        args=("$action" --dir "$install_dir")
        if [[ $action == install || $action == docker ]]; then args+=("${install_args[@]}"); fi
        had_install=false
        [[ ! -f $install_dir/.boxlist-managed ]] || had_install=true
        # Isolate failures so an action never terminates the menu loop.
        BOXLIST_MENU_CHILD=1 bash "$script_path" "${args[@]}" || true
        if [[ $action == uninstall && $had_install == true && ! -e $install_dir/.boxlist-managed ]]; then return 0; fi
        pause_panel || return 0
    done
}
docker_menu() {
    local choice action
    heading 'Docker 管理'
    printf '1、Docker 安装 BoxList\n2、进入 Docker 容器\n3、Docker 密码管理\n4、查看 Docker 状态\n5、停止 Docker 容器\n6、启动 Docker 容器\n7、重启 Docker 容器\n8、删除容器\n0、返回主菜单\n\n'
    read -r -p '请输入选项 [0-8]: ' choice || return 0
    case "$choice" in
        1) action=install ;; 2) action=shell ;; 3) action=password ;;
        4) action=status ;; 5) action=stop ;; 6) action=start ;;
        7) action=restart ;; 8) action=remove-container ;; 0) return 0 ;;
        *) fail '无效的选项' ;;
    esac
    local -a args=("$action" --dir "$install_dir")
    if [[ $action == install ]]; then args+=("${install_args[@]}"); fi
    BOXLIST_MENU_CHILD=1 bash "$script_path" "${args[@]}"
}
show_about() {
    printf '\n┌────────────────────────────────────────────────────┐\n│               BoxList Manage Script                │\n├────────────────────────────────────────────────────┤\n│  基于 OpenList 管理脚本交互适配                      │\n│  项目：https://github.com/hicocos/boxlist             │\n│  上游：OpenList Dev Team / ILoveScratch              │\n│  脚本许可证：MIT                                   │\n│  支持平台：Linux AMD64 / ARM64                     │\n└────────────────────────────────────────────────────┘\n\n感谢使用 BoxList 管理脚本！\n'
}
# All installed-instance operations share the same precondition, before Docker checks.
if [[ $command_name != menu && $command_name != install && $command_name != docker && $command_name != system && $command_name != about && ! -f $install_dir/.boxlist-managed ]]; then
    heading 'BoxList 未安装'
    printf '请先在主菜单选择 1 安装 BoxList。\n'
    [[ ${BOXLIST_MENU_CHILD:-0} == 1 || $command_name == status ]] && exit 0
    exit 1
fi
if [[ $command_name == about ]]; then show_about; exit 0; fi
if [[ $command_name == system ]]; then
    heading '系统状态检查'
    panel_status
    printf '\n磁盘空间：\n'; df -h /
    printf '\n内存使用：\n'; free -h
    printf '\n系统信息：\n'; uname -srmo
    exit 0
fi
if [[ $command_name == docker ]]; then docker_menu; exit 0; fi
# The documented one-click command opens the same menu as a bare invocation.
if [[ $command_name == install && $interactive == true && ${BOXLIST_MENU_CHILD:-0} != 1 ]]; then command_name=menu; fi
if [[ $command_name == menu ]]; then
    [[ $interactive == true ]] || fail '管理面板需要交互终端；自动化请指定具体命令'
    manage_panel
    exit 0
fi
[[ $command_name == install || $install_options == false ]] || fail '名称、端口、镜像等参数仅用于新安装；已有实例请修改 .env'
[[ -z $existing_data || $existing_data == reuse || $existing_data == reset ]] || fail '--existing-data 只能是 reuse 或 reset'
[[ -z $confirm_delete || $existing_data == reset ]] || fail '--confirm-delete 必须与 --existing-data reset 一起使用'

command -v curl >/dev/null || fail '请先安装 curl'
command -v flock >/dev/null || fail '请先安装 util-linux（flock）'
command -v timeout >/dev/null || fail '请先安装 coreutils（timeout）'
if ! command -v docker >/dev/null; then
    [[ $command_name == install ]] || fail '未安装 Docker'
    if [[ $install_docker == false && $interactive == true ]]; then
        read -r -p '未安装 Docker，是否使用 Docker 官方脚本安装？[y/N]: ' answer
        [[ $answer == y || $answer == Y ]] && install_docker=true
    fi
    [[ $install_docker == true ]] || fail '未安装 Docker；安装后重试，或传入 --install-docker'
    [[ $EUID == 0 ]] || fail '安装 Docker 需要 root，请使用 sudo bash 运行'
    docker_installer=$(mktemp)
    trap 'rm -f -- "$docker_installer"' EXIT
    curl -fsSL https://get.docker.com -o "$docker_installer"
    sh "$docker_installer"
    rm -f -- "$docker_installer"
    trap - EXIT
fi
docker info >/dev/null 2>&1 || fail '无法连接 Docker；请检查服务、权限或 Docker context'
docker compose version >/dev/null 2>&1 || fail '请先安装 Docker Compose 插件（docker compose）'
# Read daemon details, since a Docker context can point at a different host.
case $(docker info --format '{{.OSType}}/{{.Architecture}}') in
    linux/x86_64|linux/amd64|linux/aarch64|linux/arm64) ;;
    *) fail '镜像仅支持 Linux AMD64 / ARM64 Docker 主机' ;;
esac

check_volume_unused() {
    local users
    users=$(docker ps -a --filter "volume=${instance}_data" --format '{{.Names}}')
    [[ -z $users ]] || fail "数据卷 ${instance}_data 仍被容器引用（包括已停止容器）：$users；请先处理原实例"
}

marker="$install_dir/.boxlist-managed"
if [[ $command_name == install ]]; then
    if [[ -e $marker ]]; then
        fail "此目录已经安装；使用 update 或 start：$install_dir"
    fi
    if [[ $interactive == true ]]; then
        heading '安装 BoxList'
        printf '按 Enter 使用默认值；端口和站点地址以后也能在面板修改。\n'
        [[ $port_explicit == true ]] || prompt_port
        [[ $bind_explicit == true ]] || prompt_bind
        [[ $site_explicit == true ]] || prompt_site
        printf '\n  实例：%s\n  目录：%s\n  监听：%s:%s\n  镜像：%s\n\n' "$instance" "$install_dir" "$bind" "$port" "$image"
        read -r -p '开始安装？[Y/n]: ' answer || exit 0
        case "$answer" in n|N) printf '已取消。\n'; exit 0 ;; esac
    fi
    [[ $instance =~ ^[a-z0-9][a-z0-9_-]*$ ]] || fail '名称仅允许小写字母、数字、下划线和短横线'
    valid_port "$port" || fail '端口必须为 1–65535'
    valid_bind "$bind" || fail '无效 IPv4 地址'
    [[ $image =~ ^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$ ]] || fail '无效镜像地址'
    valid_site "$site_url" || fail '站点地址须为有效 HTTPS URL 或留空'
    if docker container inspect "$instance" >/dev/null 2>&1; then
        fail "容器 $instance 已存在；不会接管或覆盖已有实例"
    fi
    [[ -z $(docker ps -aq --filter "label=com.docker.compose.project=$instance") ]] || fail "Compose 项目 $instance 已存在"
    if [[ -d $install_dir ]]; then
        shopt -s nullglob dotglob
        entries=("$install_dir"/*)
        (( ${#entries[@]} == 0 )) || fail "安装目录非空，不会覆盖：$install_dir"
        shopt -u nullglob dotglob
    fi
    if docker volume inspect "${instance}_data" >/dev/null 2>&1; then
        check_volume_unused
        printf '检测到残留数据卷：%s\n' "${instance}_data"
        if [[ -z $existing_data ]]; then
            [[ $interactive == true ]] || fail '请指定 --existing-data reuse 保留数据，或 --existing-data reset --confirm-delete 完整数据卷名 删除重装'
            printf '1) 保留数据重新安装（保留账号、设置和预览图）\n2) 删除数据全新安装（不可恢复）\n0) 取消（默认）\n'
            read -r -p '请选择 [0-2]: ' data_choice || exit 0
            case "$data_choice" in
                1) existing_data=reuse ;;
                2) existing_data=reset ;;
                0|'') printf '已取消，数据未修改。\n'; exit 0 ;;
                *) fail '无效选择，数据未修改' ;;
            esac
        fi
        if [[ $existing_data == reset ]]; then
            printf '将删除 %s 中的全部账号、配置、数据库和本地预览图。\n' "${instance}_data"
            if [[ -z $confirm_delete && $interactive == true ]]; then
                read -r -p "请输入完整数据卷名 ${instance}_data 确认删除：" confirm_delete || exit 1
            fi
            [[ $confirm_delete == "${instance}_data" ]] || fail '删除确认不匹配，数据未修改（--yes 不代替此确认）'
            reset_volume=true
        else
            reuse_volume=true
        fi
    fi
    # Fetch before changing local files or deleting any residual data.
    docker pull "$image"
    mkdir -p -- "$install_dir"
else
    [[ -f $marker && -f $install_dir/compose.yaml && -f $install_dir/.env ]] || fail "未找到脚本管理的安装：$install_dir"
    IFS= read -r instance < "$marker"
    [[ $instance =~ ^[a-z0-9][a-z0-9_-]*$ ]] || fail '安装标记无效'
fi

exec 9>"$install_dir/.boxlist.lock"
flock -n 9 || fail '另一个管理操作正在运行'
compose() {
    # Persisted .env wins over unrelated caller environment variables.
    local files=(-f "$install_dir/compose.yaml")
    if [[ -f $install_dir/compose.override.yaml ]]; then files+=(-f "$install_dir/compose.override.yaml"); fi
    if [[ -f $install_dir/compose.volume.yaml ]]; then files+=(-f "$install_dir/compose.volume.yaml"); fi
    env -u BOXLIST_IMAGE -u BOXLIST_CONTAINER_NAME -u PORT -u BIND_ADDRESS -u SITE_URL \
        docker compose --project-directory "$install_dir" --env-file "$install_dir/.env" \
        --project-name "$instance" "${files[@]}" "$@"
}
check_owner() {
    local id directory
    while IFS= read -r id; do
        [[ -n $id ]] || continue
        directory=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$id")
        [[ $directory == "$install_dir" ]] || fail "项目 $instance 属于其他目录：$directory"
    done < <(docker ps -aq --filter "label=com.docker.compose.project=$instance")
    if docker container inspect "$instance" >/dev/null 2>&1; then
        directory=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$instance")
        [[ $directory == "$install_dir" ]] || fail "容器 $instance 不属于本安装"
    fi
}
ready() {
    local id attempt
    id=$(compose ps -q boxlist)
    [[ -n $id ]] || fail '未找到运行中的 BoxList 容器'
    # Probe inside the container; no dependency on host port/firewall/context.
    for (( attempt=0; attempt<30; attempt++ )); do
        # The inner shell expands status after reading the HTTP response.
        # shellcheck disable=SC2016
        if timeout 3 docker exec "$id" bash -c '
            exec 3<>/dev/tcp/127.0.0.1/5244
            printf "GET /api/public/settings HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n" >&3
            IFS= read -r status <&3
            [[ $status == *" 200 "* ]]
        ' >/dev/null 2>&1; then
            printf 'BoxList 已启动；端口映射：\n'
            compose port boxlist 5244
            return
        fi
        sleep 1
    done
    fail '30 次检查后 API 未就绪；请使用 logs 查看原因（日志可能包含密码）'
}

show_access() {
    local address
    address=$(compose port boxlist 5244)
    printf '  端口映射  %s\n' "$address"
    if [[ $address == 0.0.0.0:* ]]; then
        printf '  访问地址  http://<服务器IP>:%s\n' "${address##*:}"
    else
        printf '  访问地址  http://%s\n' "$address"
    fi
    [[ -z $site_url ]] || printf '  站点地址  %s\n' "$site_url"
    return 0
}
show_initial_credentials() {
    local line password=
    while IFS= read -r line; do
        case "$line" in
            *'Successfully created the admin user and the initial password is: '*)
                password=${line##*initial password is: } ;;
        esac
    done < <(compose logs --no-color boxlist)
    if [[ -n $password ]]; then
        printf '\n  管理员账号  admin\n  初始密码    %s%s%s\n\n  请保存密码，登录后及时修改。\n' "$success" "$password" "$reset"
    else
        printf '\n  已保留原账号与密码，未进行重置。\n  忘记密码可在管理面板选择“重置管理员密码”。\n'
    fi
}
read_settings() {
    local line key value
    while IFS= read -r line || [[ -n $line ]]; do
        [[ $line == *=* ]] || continue
        key=${line%%=*}; value=${line#*=}
        if [[ $value == \"*\" || $value == \'*\' ]]; then value=${value:1:${#value}-2}; fi
        case "$key" in PORT) port=$value ;; BIND_ADDRESS) bind=$value ;; SITE_URL) site_url=$value ;; esac
    done < "$install_dir/.env"
}
configure() {
    [[ $interactive == true ]] || fail '修改配置需要交互终端'
    read_settings
    heading '访问配置'
    prompt_port; prompt_bind; prompt_site
    read -r -p '保存并重新创建容器？[y/N]: ' answer || return 0
    [[ $answer == y || $answer == Y ]] || return 0
    # EXIT traps run after function locals unwind on errexit.
    local line
    previous=$(mktemp "$install_dir/.env.previous.XXXXXX")
    cp -p -- "$install_dir/.env" "$previous"
    rollback_config() {
        local result=$?
        trap - EXIT
        if (( result != 0 )); then
            cp -p -- "$previous" "$install_dir/.env"
            printf '配置应用失败，已还原原配置，尝试恢复容器。\n' >&2
            compose up -d --no-build || true
        fi
        rm -f -- "$previous" "$install_dir/.env.next"
        exit "$result"
    }
    trap rollback_config EXIT
    umask 077
    while IFS= read -r line || [[ -n $line ]]; do
        case "$line" in PORT=*|BIND_ADDRESS=*|SITE_URL=*) continue ;; esac
        printf '%s\n' "$line"
    done < "$previous" > "$install_dir/.env.next"
    printf "PORT='%s'\nBIND_ADDRESS='%s'\nSITE_URL='%s'\n" "$port" "$bind" "$site_url" >> "$install_dir/.env.next"
    mv -- "$install_dir/.env.next" "$install_dir/.env"
    compose config -q
    compose up -d --no-build
    ready
    rm -f -- "$previous"
    trap - EXIT
    show_access
}
backup_data() {
    umask 077
    mkdir -p -- "$install_dir/backups"
    destination=$(mktemp "$install_dir/backups/data-$(date +%Y%m%d-%H%M%S)-XXXXXX.tar.gz")
    running=$(compose ps --status running -q boxlist)
    finish_backup() {
        local result=$?
        trap - EXIT
        [[ -z $running ]] || compose start boxlist
        if (( result != 0 )); then rm -f -- "$destination"; fi
        exit "$result"
    }
    trap finish_backup EXIT
    printf '正在暂停服务并备份应用数据…\n'
    compose stop boxlist
    compose run --rm --no-deps -T --user 0 --entrypoint tar boxlist -C /opt/openlist/data -czf - . > "$destination"
    [[ -z $running ]] || compose start boxlist
    trap - EXIT
    printf '备份完成：%s\n包含应用数据库和数据目录；不包含外部原文件及 S3 桶对象。\n' "$destination"
}
update_script() {
    download=$(mktemp "$install_dir/.script.XXXXXX")
    trap 'rm -f -- "$download"' EXIT
    curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/install.sh -o "$download"
    bash -n "$download"
    chmod 700 "$download"
    mv -- "$download" "$install_dir/boxlist.sh"
    trap - EXIT
    printf '管理脚本已更新。\n'
}
restore_data() {
    [[ $interactive == true ]] || fail '恢复配置需要交互终端'
    local choice path entry
    local -a backups
    heading '配置恢复'
    shopt -s nullglob
    backups=("$install_dir"/backups/*.tar.gz)
    shopt -u nullglob
    for (( choice=0; choice<${#backups[@]}; choice++ )); do printf '%d、%s\n' "$((choice+1))" "${backups[$choice]##*/}"; done
    printf 'x、自定义输入备份路径\n0、返回主菜单\n'
    read -r -p '请选择备份: ' choice || return 0
    case "$choice" in
        0|'') return 0 ;;
        x|X) read -r -p '请输入备份文件路径: ' path || return 0 ;;
        *) [[ $choice =~ ^[1-9][0-9]*$ && ${#choice} -lt 6 ]] || fail '无效选择'
           (( choice <= ${#backups[@]} )) || fail '无效选择'
           path=${backups[$((choice-1))]} ;;
    esac
    [[ -f $path ]] || fail '备份文件不存在'
    # Accept only regular files and directories from our data-only archives.
    archive_listing=$(tar -tzf "$path")
    while IFS= read -r entry; do
        [[ $entry != /* && /$entry/ != */../* ]] || fail '备份含有不安全路径'
    done <<< "$archive_listing"
    archive_types=$(tar -tvzf "$path")
    while IFS= read -r entry; do [[ $entry == -* || $entry == d* ]] || fail '备份含有链接或特殊文件'; done <<< "$archive_types"
    printf '恢复将覆盖当前应用数据；操作前会自动备份当前数据。\n'
    read -r -p '确认恢复？[y/N]: ' answer || return 0
    [[ $answer == y || $answer == Y ]] || return 0
    backup_data
    restore_backup=$destination
    restore_running=$(compose ps --status running -q boxlist)
    recover_restore() {
        local result=$?
        trap - EXIT
        if (( result != 0 )); then
            printf '恢复失败，正在恢复操作前的数据。\n' >&2
            compose run --rm --no-deps -T --user 0 --entrypoint sh boxlist -c 'find /opt/openlist/data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; tar -xzf - -C /opt/openlist/data' < "$restore_backup" || true
        fi
        [[ -z $restore_running ]] || compose start boxlist
        exit "$result"
    }
    trap recover_restore EXIT
    compose stop boxlist
    compose run --rm --no-deps -T --user 0 --entrypoint sh boxlist -ec 'find /opt/openlist/data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +; tar -xzf - -C /opt/openlist/data' < "$path"
    [[ -z $restore_running ]] || compose start boxlist
    trap - EXIT
    printf '恢复成功。操作前备份：%s\n' "$restore_backup"
}
valid_schedule() {
    local value segment base step low high index=0
    local -a fields parts limits=(59 23 31 12 7) minimum=(0 0 1 1 0)
    read -r -a fields <<< "$1"
    (( ${#fields[@]} == 5 )) || return 1
    for value in "${fields[@]}"; do
        [[ $value != ,* && $value != *, && $value != *,,* ]] || return 1
        IFS=, read -r -a parts <<< "$value"
        for segment in "${parts[@]}"; do
            [[ $segment =~ ^(\*|[0-9]{1,2}(-[0-9]{1,2})?)(/[0-9]{1,2})?$ ]] || return 1
            base=${segment%%/*}
            if [[ $segment == */* ]]; then
                step=${segment##*/}; (( 10#$step > 0 && 10#$step <= limits[index]+1 )) || return 1
            fi
            if [[ $base != '*' ]]; then
                low=${base%-*}; high=${base##*-}
                (( 10#$low >= minimum[index] && 10#$high <= limits[index] && 10#$low <= 10#$high )) || return 1
            fi
        done
        index=$((index+1))
    done
}
schedule_update() {
    [[ $interactive == true ]] || fail '定时更新设置需要交互终端'
    [[ $EUID == 0 ]] || fail '定时更新设置需要 root 权限'
    command -v crontab >/dev/null || fail '请先安装并启用 cron 服务'
    local choice schedule line
    local cron_file="/etc/cron.d/boxlist-${instance}"
    heading '设置定时自动更新'
    printf '1、启用定时更新\n2、禁用定时更新\n3、查看当前设置\n0、返回主菜单\n\n'
    read -r -p '请输入选项 [0-3]: ' choice || return 0
    case "$choice" in
        1)
            printf '设置更新时间（cron 格式）\n默认：每周日凌晨2点 (0 2 * * 0)\n示例：每天凌晨3点 (0 3 * * *)\n'
            read -r -p '请输入 cron 时间表达式 (默认: 0 2 * * 0): ' schedule || return 0
            schedule=${schedule:-'0 2 * * 0'}
            [[ $schedule =~ ^[0-9*/,-]+[[:blank:]]+[0-9*/,-]+[[:blank:]]+[0-9*/,-]+[[:blank:]]+[0-9*/,-]+[[:blank:]]+[0-9*/,-]+$ ]] || fail '无效 cron 表达式'
            valid_schedule "$schedule" || fail 'cron 时间超出范围或格式无效'
            [[ $install_dir != *%* && $install_dir != *\'* ]] || fail '安装路径包含 cron 不支持的字符'
            printf -v line "%s root /bin/bash '%s/boxlist.sh' update --dir '%s' --non-interactive >> '%s/update.log' 2>&1" "$schedule" "$install_dir" "$install_dir" "$install_dir"
            cron_tmp=$(mktemp)
            trap 'rm -f -- "$cron_tmp"' EXIT
            printf 'SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n%s\n' "$line" > "$cron_tmp"
            install -m 644 "$cron_tmp" "$cron_file"
            rm -f -- "$cron_tmp"; trap - EXIT
            printf '定时更新已启用\n更新时间：%s（服务器本地时间）\n' "$schedule" ;;
        2) rm -f -- "$cron_file"; printf '定时更新已禁用\n' ;;
        3) if [[ -f $cron_file ]]; then cat "$cron_file"; else printf '未设置定时更新\n'; fi ;;
        0|'') return 0 ;; *) fail '无效的选项' ;;
    esac
}

check_owner
case "$command_name" in
    install)
        umask 077
        curl -fsSL https://raw.githubusercontent.com/hicocos/boxlist/main/compose.yaml -o "$install_dir/compose.yaml.tmp"
        mv -- "$install_dir/compose.yaml.tmp" "$install_dir/compose.yaml"
        printf "BOXLIST_CONTAINER_NAME='%s'\nBOXLIST_IMAGE='%s'\nPORT='%s'\nBIND_ADDRESS='%s'\nSITE_URL='%s'\n" \
            "$instance" "$image" "$port" "$bind" "$site_url" > "$install_dir/.env"
        if [[ $reuse_volume == true ]]; then
            # External volumes retain their original ownership labels.
            printf 'volumes:\n  data:\n    external: true\n    name: %s_data\n' "$instance" > "$install_dir/compose.volume.yaml"
        fi
        compose config -q
        cp -- "$script_path" "$install_dir/boxlist.sh"
        chmod 700 "$install_dir/boxlist.sh"
        if [[ $reset_volume == true ]]; then
            check_volume_unused
            docker volume rm "${instance}_data"
        elif [[ $reuse_volume == true ]]; then
            check_volume_unused
            docker volume inspect "${instance}_data" >/dev/null
        fi
        printf '%s\n' "$instance" > "$marker"
        compose up -d --no-build
        ready
        if [[ $EUID == 0 && $instance == boxlist ]]; then
            if [[ ! -e /usr/local/bin/boxlist && ! -L /usr/local/bin/boxlist ]]; then
                # Positional parameters belong to the generated launcher.
                # shellcheck disable=SC2016
                printf '#!/usr/bin/env bash\nexec bash %q "${1:-menu}" --dir %q "${@:2}"\n' \
                    "$install_dir/boxlist.sh" "$install_dir" > /usr/local/bin/boxlist
                chmod 755 /usr/local/bin/boxlist
            else
                printf '已有 /usr/local/bin/boxlist，未覆盖。\n'
            fi
        fi
        heading '安装完成'
        show_access
        show_initial_credentials
        printf '\n  管理命令  bash %q --dir %q\n' "$install_dir/boxlist.sh" "$install_dir"
        ;;
    update)
        printf '更新将保留数据卷与配置；请确保已完成备份。\n'
        compose pull
        compose up -d --no-build
        ready ;;
    start) compose up -d --no-build; ready ;;
    stop) compose stop ;;
    restart) compose restart; ready ;;
    status)
        heading '服务状态'
        compose ps -a
        id=$(compose ps --status running -q boxlist)
        if [[ -n $id ]]; then docker stats --no-stream --format 'CPU {{.CPUPerc}}  内存 {{.MemUsage}}  网络 {{.NetIO}}' "$id"; fi ;;
    logs) compose logs --tail 100 boxlist ;;
    configure) configure ;;
    backup) backup_data ;;
    restore) restore_data ;;
    schedule) schedule_update ;;
    shell) compose exec boxlist /bin/sh ;;
    remove-container)
        read -r -p '确认删除容器？[y/N]: ' answer || exit 0
        [[ $answer == y || $answer == Y ]] || exit 0
        compose down
        printf '容器已删除，应用数据保留。彻底卸载请使用主菜单 3。\n' ;;
    script-update) update_script ;;
    password)
        password_args=(random)
        if [[ $interactive == true ]]; then
            heading '密码管理'
            printf '1、生成随机密码\n2、设置新密码\n3、查看当前账号信息\n4、从日志中提取初始密码\n5、重置数据库（危险操作）\n0、返回主菜单\n\n'
            read -r -p '请输入选项 [0-5]: ' choice || exit 0
            case "$choice" in
                1) ;;
                2)
                    while true; do
                        read -r -s -p '输入新密码：' new_password || exit 0; printf '\n'
                        read -r -s -p '再次输入：' repeated_password || exit 0; printf '\n'
                        if [[ -n $new_password && $new_password == "$repeated_password" ]]; then break; fi
                        printf '密码不能为空且两次输入须一致，请重试。\n'
                    done
                    password_args=(set "$new_password") ;;
                3)
                    running=$(compose ps --status running -q boxlist)
                    compose stop boxlist
                    trap '[[ -z $running ]] || compose start boxlist' EXIT
                    compose run --rm --no-deps boxlist admin --no-prefix
                    exit 0 ;;
                4) show_initial_credentials; exit 0 ;;
                5)
                    printf '警告：此操作将清空应用数据和配置，操作前自动备份。\n'
                    read -r -p "确认重置？请输入 'RESET': " answer || exit 0
                    [[ $answer == RESET ]] || exit 0
                    backup_data
                    running=$(compose ps --status running -q boxlist)
                    compose stop boxlist
                    trap '[[ -z $running ]] || compose start boxlist' EXIT
                    compose run --rm --no-deps -T --user 0 --entrypoint sh boxlist -ec 'find /opt/openlist/data -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +'
                    compose up -d --no-build --force-recreate
                    ready
                    show_initial_credentials
                    trap - EXIT
                    exit 0 ;;
                0|'') exit 0 ;;
                *) fail '无效选择' ;;
            esac
            read -r -p '将短暂停服并重置管理员密码，继续？[y/N]: ' answer || exit 0
            [[ $answer == y || $answer == Y ]] || exit 0
        fi
        # Do not run a second database writer alongside the server.
        running=$(compose ps --status running -q boxlist)
        compose stop boxlist
        restore_service() { if [[ -n $running ]]; then compose start boxlist; fi; }
        trap restore_service EXIT
        compose run --rm --no-deps boxlist admin "${password_args[@]}" --no-prefix
        restore_service
        trap - EXIT
        [[ -z $running ]] || ready ;;
    uninstall)
        heading '彻底卸载 BoxList'
        printf '将删除：\n  · 实例 %s 的容器和网络\n  · 数据卷 %s_data（账号、设置和本地预览图）\n  · 安装目录 %s（包含其中的备份）\n  · 对应的 boxlist 管理命令\n\n外部挂载原文件、远端网盘及其他实例不受影响。\n' "$instance" "$instance" "$install_dir"
        if [[ $yes != true ]]; then
            read -r -p "此操作不可恢复，输入实例名 $instance 确认：" answer || exit 1
            [[ $answer == "$instance" ]] || { printf '已取消。\n'; exit 0; }
        fi
        ids=$(compose ps -aq boxlist)
        for id in $ids; do
            mount=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/opt/openlist/data"}}{{.Type}}:{{.Name}}{{end}}{{end}}' "$id")
            [[ $mount == "volume:${instance}_data" ]] || fail '此实例使用自定义数据挂载，请手动处理，未执行卸载'
        done
        # Refuse to remove data shared by an unrelated or stopped container.
        while IFS= read -r id; do
            [[ -n $id ]] || continue
            [[ " $ids " == *" $id "* || $'\n'"$ids"$'\n' == *$'\n'"$id"$'\n'* ]] || fail '数据卷被其他容器引用，未执行卸载'
        done < <(docker ps -aq --no-trunc --filter "volume=${instance}_data")
        if [[ $EUID == 0 ]]; then rm -f -- "/etc/cron.d/boxlist-${instance}"; fi
        compose down
        if docker volume inspect "${instance}_data" >/dev/null 2>&1; then docker volume rm "${instance}_data"; fi
        if [[ -f /usr/local/bin/boxlist ]]; then
            launcher=$(cat /usr/local/bin/boxlist)
            # Match literal parameters in our generated launcher only.
            # shellcheck disable=SC2016
            printf -v expected '#!/usr/bin/env bash\nexec bash %q "${1:-menu}" --dir %q "${@:2}"' "$install_dir/boxlist.sh" "$install_dir"
            if [[ $launcher == "$expected" ]]; then rm -f /usr/local/bin/boxlist; fi
        fi
        rm -rf -- "$install_dir"
        printf '%sBoxList 已彻底卸载。%s\n' "$success" "$reset" ;;
esac

# Never hold the operation lock while waiting for the next menu choice.
exec 9>&-
