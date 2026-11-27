#!/usr/bin/env bash
set -euo pipefail
umask 022

artifacts=${1:?请提供构建产物目录}
revision=${2:?请提供提交 SHA}
gitea_host=${3:-}
repository=${4:-}
root=${GRADE_RELEASE_ROOT:-/opt/grade-business}
units=${GRADE_RELEASE_UNITS:-/etc/systemd/system}
service=grade-business.service
binary="$root/grade-business"
bootstrap="$root/bootstrap.env"
override="$units/$service.d/zz-grade-business-release.conf"
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# 用途 输出部署故障并让流水线失败。
# 参数 $1 为不含凭据的故障说明。
# 返回值 无，以状态 1 结束进程。
fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

# 用途 选用 Ubuntu 网卡上配置的私有 IPv4 地址和前缀。
# 参数 无，优先选与 Gitea 通信的网卡，其次使用默认路由。
# 返回值 内网地址及前缀，无法唯一识别且无适用路由时返回非零。
detect_lan() {
  local route_destination=1.1.1.1
  if [[ "$gitea_host" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]]; then
    route_destination=$gitea_host
  fi
  local sources
  sources=$({
    ip -4 -o route get "$route_destination" 2>/dev/null || true
    ip -4 -o route get 1.1.1.1 2>/dev/null || true
  } | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i+1)}')
  ip -4 -o addr show up scope global | awk -v routes="$sources" '
    BEGIN {
      fields = split(routes, route, /[[:space:]]+/)
      for (i = 1; i <= fields; i++) {
        if (route[i] != "" && !(route[i] in priority)) priority[route[i]] = i
      }
    }
    {
      split($4, address, "/")
      split(address[1], octet, ".")
      private = octet[1] == 10 ||
                (octet[1] == 172 && octet[2] >= 16 && octet[2] <= 31) ||
                (octet[1] == 192 && octet[2] == 168)
      rank = priority[address[1]]
      if (private && rank > 0 && (best == 0 || rank < best)) { selected = $4; best = rank }
      if (private) { count++; fallback = $4 }
    }
    END {
      if (selected != "") print selected
      else if (count == 1) print fallback
      else exit 1
    }'
}

# 用途 判定已有但未完成初始化的旧 systemd 单元能否使用托管数据库。
# 参数 无，通过 systemd 查询目标单元当前的启动方式及数据库配置来源。
# 返回值 确认为无配置且未运行的直接启动单元时返回 0。
unit_missing_database() {
  (( previously_active == 0 )) || return 1
  local launch inline env_files pass_env
  launch=$(systemctl show "$service" -p ExecStart --value) || return 1
  [[ "$launch" == *"path=$binary ;"* ]] || return 1
  inline=$(systemctl show "$service" -p Environment --value) || return 1
  env_files=$(systemctl show "$service" -p EnvironmentFiles --value) || return 1
  pass_env=$(systemctl show "$service" -p PassEnvironment --value) || return 1
  [[ "$inline" != *GRADE_BUSINESS_DSN=* && -z "$env_files" &&
    "$pass_env" != *GRADE_BUSINESS_DSN* ]]
}

# 用途 首次创建业务库及凭据，重试时沿用原凭据和数据卷。
# 参数 无，读取当前发布目录及首次部署端口。
# 返回值 数据库就绪时返回 0，初始化失败时中止发布。
start_database() {
  if [[ ! -f "$bootstrap" ]]; then
    local db_port=${GRADE_RELEASE_DB_PORT:-15445}
    local app_port=${GRADE_RELEASE_APP_PORT:-18444}
    [[ "$db_port" =~ ^[1-9][0-9]{0,4}$ && "$app_port" =~ ^[1-9][0-9]{0,4}$ ]] &&
      (( db_port <= 65535 && app_port <= 65535 && db_port != app_port )) ||
      fail '请为数据库和应用配置不同的有效端口'
    if docker volume inspect grade-business-release_business-data >/dev/null 2>&1; then
      fail '已存在发布数据库卷，请恢复原 bootstrap.env 后重试'
    fi
    local password
    password=$(openssl rand -hex 32)
    (
      umask 077
      cat > "$transaction/bootstrap.env" <<EOF
GRADE_RELEASE_DB_PASSWORD=$password
GRADE_RELEASE_DB_PORT=$db_port
GRADE_BUSINESS_DSN=postgres://grade_business:$password@127.0.0.1:$db_port/grade_business?sslmode=disable
EOF
      if [[ "$unit_state" == not-found ]]; then
        printf 'GRADE_BUSINESS_ADDR=127.0.0.1:%s\n' "$app_port" >> "$transaction/bootstrap.env"
      fi
    )
    mv -T -- "$transaction/bootstrap.env" "$bootstrap"
  fi

  env -u GRADE_RELEASE_DB_PASSWORD -u GRADE_RELEASE_DB_PORT docker compose \
    --env-file "$bootstrap" -f "$source_dir/compose.release.yaml" \
    up -d --wait --wait-timeout 90
}

# 用途 为未配置业务服务的 Ubuntu 主机安装可开机启动的 systemd 单元。
# 参数 无，使用首次部署的数据库配置和系统状态目录。
# 返回值 成功写入单元时返回 0，写入失败时中止发布。
install_unit() {
  cat > "$transaction/service" <<EOF
[Unit]
Description=Grade Business

[Service]
WorkingDirectory=$root
Environment=GRADE_BUSINESS_ATTACHMENT_DIR=/var/lib/grade-business/attachments
ExecStart=$binary
DynamicUser=yes
StateDirectory=grade-business
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  install -m 0644 "$transaction/service" "$units/$service"
}

# 用途 检查本次构建的进程、数据库和前端首页均可用。
# 参数 无，读取 systemd 主进程的实际监听地址并使用发布产物校验。
# 返回值 检查通过返回 0，进程未就绪或响应不一致返回非零。
check_release() {
  local pid address ready
  pid=$(systemctl show "$service" -p MainPID --value) || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ && "/proc/$pid/exe" -ef "$binary" ]] || return 1
  address=$(tr '\0' '\n' < "/proc/$pid/environ" | sed -n 's/^GRADE_BUSINESS_ADDR=//p') ||
    return 1
  address=${address:-127.0.0.1:18444}
  endpoint="http://${lan%/*}:${address##*:}"
  ready=$(curl --noproxy '*' -fsS --max-time 3 "$endpoint/readyz" 2>/dev/null) ||
    return 1
  [[ "$ready" =~ \"status\"[[:space:]]*:[[:space:]]*\"ready\" ]] || return 1
  curl --noproxy '*' -fsS --max-time 3 "$endpoint/" -o "$transaction/index" 2>/dev/null &&
    cmp -s "$transaction/index" "$web/index.html" &&
    curl --noproxy '*' -fsS --max-time 3 "$endpoint$entry" -o "$transaction/entry" 2>/dev/null &&
    cmp -s "$transaction/entry" "$web$entry"
}

# 用途 发布失败时恢复原有业务进程和前端配置，并清理本次临时文件。
# 参数 无，通过退出状态判断是否需要恢复。
# 返回值 保留发布退出状态。恢复异常时保留备份目录供定位。
finish_release() {
  local status=$?
  trap - EXIT
  if (( status != 0 && switched == 1 )); then
    local restored=0
    systemctl stop "$service" || restored=1
    if [[ -e "$transaction/previous-binary" || -L "$transaction/previous-binary" ]]; then
      cp -a -- "$transaction/previous-binary" "$transaction/restore-binary" &&
        mv -Tf -- "$transaction/restore-binary" "$binary" || restored=1
    else
      rm -f -- "$binary" || restored=1
    fi
    if [[ -f "$transaction/previous-override" ]]; then
      cp -a -- "$transaction/previous-override" "$override" || restored=1
    else
      rm -f -- "$override" || restored=1
    fi
    systemctl daemon-reload || restored=1
    if (( restored == 0 && previously_active == 1 )); then
      systemctl start "$service" || restored=1
    fi
    if (( restored != 0 )); then
      printf '回退失败，备份保留在 %s\n' "$transaction" >&2
      return "$status"
    fi
  fi
  if (( status != 0 )) && [[ -n "$web" ]]; then
    rm -rf -- "$web"
  fi
  rm -rf -- "$transaction"
  return "$status"
}

(( EUID == 0 )) || fail '请以 root 或免交互 sudo 执行 Ubuntu 发布脚本'
[[ "$revision" =~ ^[a-f0-9]{40}$ ]] || fail '提交 SHA 格式无效'
[[ "$root" =~ ^/[a-zA-Z0-9_./-]+$ && "$units" =~ ^/[a-zA-Z0-9_./-]+$ ]] ||
  fail '发布路径必须是不含空格的绝对路径'
[[ -x "$artifacts/grade-business" && -s "$artifacts/web/index.html" ]] ||
  fail '缺少后端或前端构建产物'
entry=$(sed -nE 's/.*src="(\/assets\/[a-zA-Z0-9_.-]+\.js)".*/\1/p' "$artifacts/web/index.html")
[[ -n "$entry" && -s "$artifacts/web$entry" ]] || fail '缺少前端入口脚本'
lan=$(detect_lan) || fail '无法确定服务器内网 IPv4 地址及网段'

mkdir -p "$root/web-releases"
exec 9>"$root/.deploy.lock"
flock -w 120 9 || fail '已有其他部署正在进行'
if [[ -n "$repository" ]]; then
  latest=$(git ls-remote --exit-code "$repository" refs/heads/main)
  if [[ "${latest%%$'\t'*}" != "$revision" ]]; then
    echo 'main 已更新，跳过过期部署'
    exit 0
  fi
fi
transaction=$(mktemp -d "$root/.deploy.XXXXXX")
switched=0
previously_active=0
web=
trap finish_release EXIT

unit_state=$(systemctl show "$service" -p LoadState --value) ||
  [[ "$unit_state" == not-found ]] || fail '无法读取 systemd 服务配置'
case "$unit_state" in
  loaded|not-found) ;;
  *) fail "无法发布到 systemd 服务状态 $unit_state" ;;
esac
if systemctl is-active --quiet "$service"; then
  previously_active=1
fi
managed_database=0
if [[ "$unit_state" == not-found || -f "$bootstrap" ]] || unit_missing_database; then
  start_database
  managed_database=1
fi
if [[ "$unit_state" == not-found ]]; then
  install_unit
fi

web=$(mktemp -d "$root/web-releases/$revision.XXXXXX")
chmod 0755 "$web"
cp -R -- "$artifacts/web/." "$web/"
install -m 0755 "$artifacts/grade-business" "$transaction/next-binary"
mkdir -p "$units/$service.d"
if [[ -e "$binary" || -L "$binary" ]]; then
  cp -a -- "$binary" "$transaction/previous-binary"
fi
if [[ -f "$override" ]]; then
  cp -a -- "$override" "$transaction/previous-override"
fi
{
  printf '[Unit]\nWants=network-online.target\nAfter=network-online.target\n'
  if (( managed_database == 1 )); then
    printf 'Wants=docker.service\nAfter=docker.service\n'
  fi
  printf '\n[Service]\nEnvironmentFile=%s/frontend.env\n' "$web"
  if (( managed_database == 1 )); then
    printf 'EnvironmentFile=%s\n' "$bootstrap"
  fi
} > "$transaction/next-override"
printf 'GRADE_BUSINESS_WEB_DIR=%s\nGRADE_BUSINESS_LAN_CIDR=%s\n' "$web" "$lan" > "$web/frontend.env"

switched=1
mv -Tf -- "$transaction/next-binary" "$binary"
install -m 0644 "$transaction/next-override" "$override"
systemctl daemon-reload
systemctl restart "$service"

for attempt in {1..30}; do
  if check_release; then
    if (( managed_database == 1 )); then
      systemctl enable "$service"
    fi
    printf '部署成功 %s，内网入口 %s，数据库与前端已通过验证\n' "$revision" "$endpoint"
    exit 0
  fi
  sleep 2
done
systemctl show "$service" -p ActiveState -p SubState -p Result || true
fail '部署后健康检查超时，正在恢复原有服务'
