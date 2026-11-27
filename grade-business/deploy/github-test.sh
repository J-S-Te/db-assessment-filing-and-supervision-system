#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

deploy_dir=/opt/db-assessment-filing-and-supervision-system
project=db-assessment-filing-and-supervision-system-test
registry=jszn666test-registry-vpc.cn-hangzhou.cr.aliyuncs.com
image_repository=$registry/full_lifecycle_management/db-assessment-filing-and-supervision-system

# 用途 使用独立项目名和保存在部署目录的配置调用 Compose。
# 参数 release 为发布目录，后续参数原样传给 Compose。
# 返回值 Compose 的退出状态。
compose_release() {
  local release=$1
  shift
  docker compose --project-name "$project" --project-directory "$deploy_dir" \
    --env-file "$deploy_dir/.env" --env-file "$release/image.env" \
    -f "$release/compose.yaml" "$@"
}

# 用途 首次发布生成数据库口令，已有数据库时要求沿用原配置。
# 参数 无。
# 返回值 配置存在且有效时成功，无法安全生成时失败。
prepare_database_config() {
  local config="$deploy_dir/.env" volume temporary
  if [[ -f "$config" && ! -L "$config" && -O "$config" ]]; then
    [[ "$(stat -c '%a' "$config")" == 600 ]] || return 1
    grep -qEx 'GRADE_TEST_DB_PASSWORD=[a-f0-9]{64}' "$config"
    return
  fi
  [[ ! -e "$config" && ! -L "$config" ]] || return 1
  volume=$(docker volume ls -q --filter "label=com.docker.compose.project=$project" \
    --filter label=com.docker.compose.volume=database-data) || return 1
  [[ -z "$volume" && ! -L "$deploy_dir/current" ]] || {
    echo '已有发布或数据库卷，必须恢复原有数据库配置。' >&2
    return 1
  }
  temporary=$(mktemp "$deploy_dir/.env.XXXXXXXX") || return 1
  local password
  password=$(openssl rand -hex 32) || return 1
  printf 'GRADE_TEST_DB_PASSWORD=%s\n' "$password" > "$temporary" || return 1
  mv -T -- "$temporary" "$config"
}

# 用途 在断网的临时 PostgreSQL 恢复数据库归档。
# 参数 image 为当前数据库镜像 ID，backup 为受限备份目录。
# 返回值 实际恢复且迁移表可读时成功。
verify_database_backup() (
  local image=$1 backup=$2 name deadline=$((SECONDS + 60))
  name="grade-business-test-restore-$(basename -- "$backup")"
  docker create --name "$name" --network none --pull never \
    --tmpfs /var/lib/postgresql/data:rw -e POSTGRES_HOST_AUTH_METHOD=trust \
    -e POSTGRES_USER=grade_business -e POSTGRES_DB=grade_business "$image" > /dev/null || return 1
  trap 'docker rm --force "$name" >/dev/null' EXIT
  docker start "$name" > /dev/null || return 1
  until docker exec "$name" pg_isready -q -h 127.0.0.1 -U grade_business -d grade_business; do
    ((SECONDS < deadline)) || return 1
    sleep 1
  done
  {
    docker exec -i "$name" pg_restore --username=grade_business --dbname=grade_business \
      --no-owner --exit-on-error < "$backup/database.dump" &&
    docker exec "$name" psql -XAtw -v ON_ERROR_STOP=1 -U grade_business -d grade_business \
      -c 'SELECT 1 FROM schema_migrations LIMIT 1'
  } > "$backup/restore.log" 2>&1 || return 1
  grep -qx '1' "$backup/restore.log"
)

# 用途 保存 PostgreSQL 备份并验证归档能在隔离数据库恢复。
# 参数 release 为当前发布目录。
# 返回值 校验和、隔离恢复均通过时成功。
backup_database() {
  local release=$1 backup database image
  backup=$(mktemp -d "$deploy_dir/backups/$(date -u +%Y%m%dT%H%M%SZ).XXXXXXXX") || return 1
  database=$(compose_release "$release" ps -q postgres) || return 1
  image=$(docker inspect --format '{{.Image}}' "$database") || return 1
  compose_release "$release" exec -T postgres \
    pg_dump --username=grade_business --dbname=grade_business --format=custom \
    > "$backup/database.dump.partial" || return 1
  test -s "$backup/database.dump.partial" || return 1
  mv -- "$backup/database.dump.partial" "$backup/database.dump" || return 1
  (cd "$backup" && sha256sum database.dump > SHA256SUMS && sha256sum -c --status SHA256SUMS) ||
    return 1
  if ! verify_database_backup "$image" "$backup"; then
    printf '数据库备份恢复演练失败：%s\n' "$backup" >&2
    return 1
  fi
  printf '数据库备份已校验并完成隔离恢复：%s\n' "$backup/database.dump"
}

# 用途 核对预期镜像的容器和应用本地健康响应。
# 参数 release 为发布目录，image 为应运行的镜像摘要引用。
# 返回值 限时内容器、数据库就绪探针和首页均可用时成功。
wait_for_application() {
  local release=$1 image=$2 container state deadline=$((SECONDS + 90))
  until ((SECONDS >= deadline)); do
    container=$(compose_release "$release" ps -q app) || return 1
    if [[ -n "$container" ]]; then
      state=$(docker inspect --format '{{.State.Running}} {{.Config.Image}}' "$container") ||
        return 1
      if [[ "$state" == "true $image" ]] &&
        curl --noproxy '*' -fsS --max-time 3 http://127.0.0.1:18444/readyz > /dev/null &&
        curl --noproxy '*' -fsS --max-time 3 http://127.0.0.1:18444/ > /dev/null; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

# 用途 核对候选应用已收到流水线注入的 DeepSeek 密钥。
# 参数 release 为候选发布目录。
# 返回值 候选容器已收到密钥时成功，且仅记录核验结果。
verify_deepseek_key() {
  local release=$1
  [[ -n "${DEEPSEEK_API_KEY:-}" ]] || return 0
  compose_release "$release" exec -T app sh -c 'test -n "${DEEPSEEK_API_KEY:-}"' || return 1
  echo '已核对候选应用收到 DeepSeek 运行密钥。'
}

# 用途 应用发布失败时重新启动上一版，首发失败时停止候选应用。
# 参数 candidate 为失败的发布目录，previous 为上一版目录或空值。
# 返回值 完成应用回退或停止时成功，处理失败时返回错误。
restore_application() {
  local candidate=$1 previous=$2 previous_image
  if [[ -z "$previous" ]]; then
    compose_release "$candidate" stop app
    return
  fi
  previous_image=$(sed -n 's/^GRADE_TEST_APP_IMAGE=//p' "$previous/image.env") || return 1
  compose_release "$previous" up --detach --no-deps --pull never app &&
    wait_for_application "$previous" "$previous_image"
}

# 用途 安全拉取候选镜像、备份现有数据库并切换测试环境应用。
# 参数 依次为部署目录、ACR 摘要引用、Base64 编码的 Compose 配置。
# 返回值 发布实际健康时成功，失败时返回非零。
deploy_test() {
  if [[ $# != 3 || "$1" != "$deploy_dir" || ! -d "$deploy_dir" ||
    ! -O "$deploy_dir" || ! -w "$deploy_dir" ||
    "$(realpath -e -- "$deploy_dir")" != "$deploy_dir" ]]; then
    echo '需要管理员预先创建由部署账号拥有的独立 /opt 目录。' >&2
    return 1
  fi

  local image=$2 payload=$3 candidate previous=''
  local digest=${image#"$image_repository@sha256:"}
  [[ "$image" == "$image_repository@sha256:$digest" &&
    "$digest" =~ ^[0-9a-f]{64}$ && ${#payload} -le 16384 ]] || return 1
  unset GRADE_TEST_APP_IMAGE GRADE_TEST_DB_PASSWORD
  [[ ! -L "$deploy_dir/.deploy.lock" ]] || return 1
  exec 9>>"$deploy_dir/.deploy.lock"
  flock -w 60 9
  [[ ! -L "$deploy_dir/releases" && ! -L "$deploy_dir/backups" ]] || return 1
  mkdir -p -- "$deploy_dir/releases" "$deploy_dir/backups"
  if [[ -e "$deploy_dir/current" || -L "$deploy_dir/current" ]]; then
    [[ -L "$deploy_dir/current" ]] || return 1
    previous=$(realpath -e -- "$deploy_dir/current")
    [[ "$previous" == "$deploy_dir/releases/"* && -f "$previous/compose.yaml" &&
      -f "$previous/image.env" ]] || return 1
  fi

  docker pull "$image"
  prepare_database_config
  candidate=$(mktemp -d "$deploy_dir/releases/${digest:0:12}.XXXXXXXX")
  printf '%s' "$payload" | base64 --decode > "$candidate/compose.yaml"
  printf 'GRADE_TEST_APP_IMAGE=%s\n' "$image" > "$candidate/image.env"
  compose_release "$candidate" config --quiet
  if [[ -n "$previous" ]]; then
    backup_database "$previous"
  fi
  compose_release "$candidate" up --detach --wait --wait-timeout 90 postgres
  if compose_release "$candidate" up --detach --no-deps --pull never app &&
    verify_deepseek_key "$candidate" &&
    wait_for_application "$candidate" "$image" &&
    ln -s -- "$candidate" "$candidate/.current" &&
    mv -Tf -- "$candidate/.current" "$deploy_dir/current"; then
    printf '测试环境发布成功：%s\n' "$image"
    return 0
  fi

  echo '候选应用切换失败，尝试恢复上一版应用，保留数据库与备份。' >&2
  if ! restore_application "$candidate" "$previous"; then
    echo '应用恢复失败，需人工核查应用和数据库状态。' >&2
  fi
  return 1
}

deploy_test "$@"
