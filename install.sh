#!/usr/bin/env bash
# Boxlist Docker installer and manager. SPDX-License-Identifier: MIT
set -Eeuo pipefail

fail() { printf '错误：%s\n' "$*" >&2; exit 1; }
usage() {
    cat <<'HELP'
用法：bash install.sh [命令] [选项]
不带命令显示管理菜单。
命令：install update start stop restart status logs password uninstall
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
  --yes               确认卸载容器；始终保留数据卷和安装目录
  --help              显示帮助
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
if [[ $# -gt 0 && $1 != -* ]]; then command_name=$1; shift; fi
while (( $# )); do
    case "$1" in
        --dir|--name|--port|--bind|--site-url|--image|--existing-data|--confirm-delete)
            (( $# >= 2 )) || fail "$1 缺少参数"
            case "$1" in
                --dir) install_dir=$2 ;;
                --name) instance=$2; install_options=true ;;
                --port) port=$2; install_options=true ;;
                --bind) bind=$2; install_options=true ;;
                --site-url) site_url=$2; install_options=true ;;
                --image) image=$2; install_options=true ;;
                --existing-data) existing_data=$2; install_options=true ;;
                --confirm-delete) confirm_delete=$2; install_options=true ;;
            esac
            shift 2 ;;
        --install-docker) install_docker=true; shift ;;
        --yes) yes=true; shift ;;
        --help|-h) usage; exit 0 ;;
        *) fail "未知选项 $1；使用 --help 查看用法" ;;
    esac
done
case "$command_name" in
    menu|install|update|start|stop|restart|status|logs|password|uninstall) ;;
    *) fail "未知命令 $command_name" ;;
esac
[[ $(uname -s) == Linux ]] || fail '仅支持 Linux'
[[ $install_dir == /* && $install_dir != / && $install_dir != *$'\n'* ]] || fail '安装目录必须为非根目录的绝对路径'
install_dir=$(realpath -m -- "$install_dir")
[[ $install_dir != / ]] || fail '不能安装到根目录'
script_path=$(realpath -- "${BASH_SOURCE[0]}")

if [[ $command_name == menu ]]; then
    cat <<'MENU'
Boxlist 管理
1) 安装     2) 更新     3) 状态     4) 日志
5) 启动     6) 停止     7) 重启     8) 重置管理员密码
9) 卸载容器（保留数据）             0) 退出
MENU
    read -r -p '请选择 [0-9]: ' choice || exit 0
    case "$choice" in
        1) command_name=install ;; 2) command_name=update ;;
        3) command_name=status ;; 4) command_name=logs ;;
        5) command_name=start ;; 6) command_name=stop ;;
        7) command_name=restart ;; 8) command_name=password ;;
        9) command_name=uninstall ;; 0) exit 0 ;;
        *) fail '无效选择' ;;
    esac
fi
[[ $command_name == install || $install_options == false ]] || fail '名称、端口、镜像等参数仅用于新安装；已有实例请修改 .env'
[[ -z $existing_data || $existing_data == reuse || $existing_data == reset ]] || fail '--existing-data 只能是 reuse 或 reset'
[[ -z $confirm_delete || $existing_data == reset ]] || fail '--confirm-delete 必须与 --existing-data reset 一起使用'

command -v curl >/dev/null || fail '请先安装 curl'
command -v flock >/dev/null || fail '请先安装 util-linux（flock）'
command -v timeout >/dev/null || fail '请先安装 coreutils（timeout）'
if ! command -v docker >/dev/null; then
    [[ $command_name == install ]] || fail '未安装 Docker'
    if [[ $install_docker == false && -t 0 ]]; then
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
    [[ ! -e $marker ]] || fail "此目录已经安装；使用 update 或 start：$install_dir"
    [[ $instance =~ ^[a-z0-9][a-z0-9_-]*$ ]] || fail '名称仅允许小写字母、数字、下划线和短横线'
    if [[ ! $port =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
        fail '端口必须为 1–65535'
    fi
    [[ $bind =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail '--bind 需要 IPv4 地址'
    IFS=. read -r -a octets <<< "$bind"
    for octet in "${octets[@]}"; do
        if [[ ${#octet} -gt 3 ]] || (( 10#$octet > 255 )); then fail '无效 IPv4 地址'; fi
    done
    [[ $image =~ ^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$ ]] || fail '无效镜像地址'
    [[ -z $site_url || $site_url =~ ^https://[^[:space:]\'\"\$\`\\]+$ ]] || fail '站点地址须为 HTTPS URL，且不能含空格、引号或 shell 特殊字符'
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
            [[ -t 0 ]] || fail '请指定 --existing-data reuse 保留数据，或 --existing-data reset --confirm-delete 完整数据卷名 删除重装'
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
            if [[ -z $confirm_delete && -t 0 ]]; then
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
    [[ -n $id ]] || fail '未找到运行中的 Boxlist 容器'
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
            printf 'Boxlist 已启动；端口映射：\n'
            compose port boxlist 5244
            return
        fi
        sleep 1
    done
    fail '30 次检查后 API 未就绪；请使用 logs 查看原因（日志可能包含密码）'
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
        printf '\n安装目录：%s\n管理命令：bash %q --dir %q\n初始密码请使用 logs 查看，登录后及时修改。\n' \
            "$install_dir" "$install_dir/boxlist.sh" "$install_dir"
        ;;
    update)
        printf '更新将保留数据卷与配置；请确保已完成备份。\n'
        compose pull
        compose up -d --no-build
        ready ;;
    start) compose up -d --no-build; ready ;;
    stop) compose stop ;;
    restart) compose restart; ready ;;
    status) compose ps -a ;;
    logs) compose logs --tail 100 boxlist ;;
    password)
        # Do not run a second database writer alongside the server.
        running=$(compose ps --status running -q boxlist)
        compose stop boxlist
        restore_service() { if [[ -n $running ]]; then compose start boxlist; fi; }
        trap restore_service EXIT
        compose run --rm --no-deps boxlist admin random --no-prefix
        restore_service
        trap - EXIT
        [[ -z $running ]] || ready ;;
    uninstall)
        if [[ $yes != true ]]; then
            read -r -p '确认移除容器和网络？数据卷、配置和脚本将保留。[y/N]: ' answer || exit 1
            [[ $answer == y || $answer == Y ]] || exit 0
        fi
        compose down
        printf '容器已卸载；数据与配置保留。使用 start 可恢复运行。\n' ;;
esac
