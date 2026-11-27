# 用途 读取当前发布进程实际使用的数据库地址。
# 参数 $1 为待检查的 systemd 单元，$2 为该单元应运行的二进制路径。
# 返回值 数据库地址写入调用方的变量，目标无法确认时返回非零。
running_database_dsn() {
  local pid
  pid=$(systemctl show "$1" -p MainPID --value) || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ && "/proc/$pid/exe" -ef "$2" ]] || return 1
  tr '\0' '\n' < "/proc/$pid/environ" | sed -n 's/^GRADE_BUSINESS_DSN=//p'
}

# 用途 找出发布包中尚未写入迁移记录的 SQL 文件。
# 参数 $1 为迁移目录，$2 为按行列出的已执行文件名。
# 返回值 待执行文件名逐行输出，迁移目录不符合发布约定时返回非零。
pending_migrations() {
  local LC_ALL=C migration name
  for migration in "$1"/*.sql; do
    [[ -f "$migration" ]] || return 1
    name=${migration##*/}
    [[ "$name" =~ ^[0-9]+_[a-z0-9_]+\.sql$ ]] || return 1
    if ! grep -Fxq -- "$name" <<< "$2"; then
      printf '%s\n' "$name"
    fi
  done
}

# 用途 确认原有外置数据库已应用全部迁移，以保留无需迁移的发布方式。
# 参数 $1、$2 为运行中的 systemd 单元和二进制，$3 为本次迁移目录。
# 返回值 无待执行迁移时返回 0，无法核对或仍需外置库独立备份时中止。
verify_external_schema() {
  local dsn applied pending uri
  command -v psql >/dev/null || fail '外置数据库缺少可用的迁移记录核对工具'
  dsn=$(running_database_dsn "$1" "$2") || fail '无法确认外置数据库的运行配置'
  # 仅核对可无损映射到 libpq 环境变量的地址，避免口令进入进程参数。
  uri='^postgres(ql)?://([[:alnum:]_]+):([[:alnum:]_.~-]+)@([[:alnum:].-]+):([1-9][0-9]{0,4})'
  uri+='/([[:alnum:]_]+)(\?sslmode=(disable|allow|prefer|require|verify-ca|verify-full))?$'
  [[ "$dsn" =~ $uri && "${BASH_REMATCH[5]}" -le 65535 ]] || fail '无法安全识别外置数据库地址'
  applied=$(PGUSER="${BASH_REMATCH[2]}" PGPASSWORD="${BASH_REMATCH[3]}" PGHOST="${BASH_REMATCH[4]}" \
    PGPORT="${BASH_REMATCH[5]}" PGDATABASE="${BASH_REMATCH[6]}" PGSSLMODE="${BASH_REMATCH[8]:-prefer}" \
    PGCONNECT_TIMEOUT=5 PGOPTIONS='-c default_transaction_read_only=on' \
    psql -X -w -At -v ON_ERROR_STOP=1 -c 'SELECT name FROM schema_migrations' 2>/dev/null) ||
    fail '无法读取外置数据库迁移记录'
  pending=$(pending_migrations "$3" "$applied") || fail '发布目录中的迁移文件无效'
  [[ -z "$pending" ]] || fail '外置数据库有待执行迁移，须先独立备份并完成迁移'
}

# 用途 先确认托管库与发布配置一致，再为待执行迁移做可恢复备份。
# 参数 $1 至 $3 为发布目录、bootstrap 文件、迁移目录，$4 至 $6 为单元、二进制、原服务运行标志。
# 返回值 库已完成迁移，或备份及隔离恢复和演练均通过时返回 0。
verify_managed_schema() {
  local release_root=$1 bootstrap_file=$2 migration_dir=$3
  local service_name=$4 executable=$5 was_active=$6
  local port configured_password configured_dsn active_dsn database applied pending
  [[ -f "$bootstrap_file" && ! -L "$bootstrap_file" ]] || fail '无法确认托管库配置文件'
  port=$(sed -n 's/^GRADE_RELEASE_DB_PORT=//p' "$bootstrap_file")
  configured_password=$(sed -n 's/^GRADE_RELEASE_DB_PASSWORD=//p' "$bootstrap_file")
  configured_dsn=$(sed -n 's/^GRADE_BUSINESS_DSN=//p' "$bootstrap_file")
  [[ "$port" =~ ^[1-9][0-9]{0,4}$ && "$port" -le 65535 && "$configured_password" =~ ^[a-f0-9]{64}$ &&
    "$configured_dsn" == "postgres://grade_business:$configured_password@127.0.0.1:$port/grade_business?sslmode=disable" ]] ||
    fail '托管数据库的配置与发布约定不一致'
  if (( was_active == 1 )); then
    active_dsn=$(running_database_dsn "$service_name" "$executable") || fail '无法读取运行进程的数据库配置'
    [[ "$active_dsn" == "$configured_dsn" ]] || fail '运行进程未使用当前托管数据库'
  fi
  database=$(docker ps --no-trunc -q --filter label=com.docker.compose.project=grade-business-release \
    --filter label=com.docker.compose.service=postgres) || fail '无法定位托管数据库容器'
  [[ "$database" =~ ^[a-f0-9]{64}$ && "$(docker port "$database" 5432/tcp)" == "127.0.0.1:$port" ]] ||
    fail '托管数据库容器或绑定端口不唯一'
  applied=$(read_managed_schema "$database") || fail '无法核对托管数据库迁移记录'
  pending=$(pending_migrations "$migration_dir" "$applied") || fail '发布目录中的迁移文件无效'
  if [[ -n "$pending" ]]; then
    backup_and_rehearse "$release_root" "$database" "$migration_dir" "$applied" "$pending"
  fi
}

# 用途 读取托管 PostgreSQL 的迁移记录，兼容尚无迁移表的首次初始化。
# 参数 $1 为已经匹配到特定数据库或者独立演练环境的容器标识。
# 返回值 已执行的迁移名逐行输出，查询失败时返回非零。
read_managed_schema() {
  local table
  table=$(docker exec "$1" psql -X -U grade_business -d grade_business -At -v ON_ERROR_STOP=1 \
    -c "SELECT to_regclass('schema_migrations')") || return 1
  if [[ -n "$table" ]]; then
    docker exec "$1" psql -X -U grade_business -d grade_business -At -v ON_ERROR_STOP=1 \
      -c 'SELECT name FROM schema_migrations ORDER BY name'
  fi
}

# 用途 在独立容器中按应用的顺序和事务规则演练待执行的迁移。
# 参数 $1、$2 为独立容器和迁移目录，$3 为待执行的迁移文件名。
# 返回值 全部迁移在单一事务内成功执行时返回 0。
rehearse_migrations() {
  local name
  {
    printf '%s\n' 'SELECT pg_advisory_xact_lock(271123881);' \
      'CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());'
    while IFS= read -r name; do
      cat -- "$2/$name" || return 1
      printf "\nINSERT INTO schema_migrations(name) VALUES ('%s');\n" "$name"
    done <<< "$3"
  } | docker exec -i "$1" psql -X -q -U grade_business -d grade_business \
    --single-transaction -v ON_ERROR_STOP=1 -f - >/dev/null
}

# 用途 持久化待迁移库的备份，在不联网的临时数据库中校验完整恢复和迁移。
# 参数 $1、$2、$3 为发布目录、业务库容器、迁移目录，$4、$5 为已执行与待执行文件名。
# 返回值 只有备份已落盘且备份恢复及全部候选迁移通过时返回 0。
backup_and_rehearse() (
  local release_root=$1 database=$2 migration_dir=$3 applied=$4 pending=$5
  local backup image scratch='' result
  umask 077
  [[ ! -L "$release_root/backups" ]] || fail '备份目录不能是符号链接'
  mkdir -p -- "$release_root/backups" || fail '无法创建备份根目录'
  backup=$(mktemp -d "$release_root/backups/pre-migration.XXXXXXXX") || fail '无法创建迁移备份目录'
  trap 'if [[ -n "$scratch" ]]; then docker rm -f "$scratch" >/dev/null || exit 1; fi; rm -f -- "$backup/database.dump.partial"' EXIT
  docker exec "$database" pg_dump -U grade_business -d grade_business -Fc \
    > "$backup/database.dump.partial" 2>"$backup/pg_dump.log" || fail "托管数据库备份失败，见受限目录：$backup"
  [[ -s "$backup/database.dump.partial" ]] || fail '托管数据库备份为空'
  docker exec -i "$database" pg_restore --list < "$backup/database.dump.partial" >/dev/null ||
    fail '托管数据库备份无法读取'
  mv -- "$backup/database.dump.partial" "$backup/database.dump" || fail '无法保存备份'
  (cd -- "$backup" && sha256sum database.dump > SHA256SUMS) || fail '无法记录备份校验和'

  image=$(docker inspect --format '{{.Image}}' "$database") || fail '无法核对 PostgreSQL 镜像'
  [[ "$image" =~ ^sha256:[a-f0-9]{64}$ ]] || fail 'PostgreSQL 镜像标识无效'
  scratch=$(docker run --rm --detach --pull never --network none \
    -e POSTGRES_USER=grade_business -e POSTGRES_DB=grade_business \
    -e POSTGRES_HOST_AUTH_METHOD=trust "$image") || fail '无法启动独立 PostgreSQL 演练容器'
  for (( result=0; result<60; result++ )); do
    if docker exec "$scratch" pg_isready -h 127.0.0.1 -U grade_business -d grade_business >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  (( result < 60 )) || fail '独立 PostgreSQL 演练容器未就绪'
  docker exec -i "$scratch" pg_restore -U grade_business -d grade_business --exit-on-error \
    --no-owner --no-privileges < "$backup/database.dump" 2>"$backup/pg_restore.log" ||
    fail "备份无法在独立 PostgreSQL 中恢复，见受限目录：$backup"
  result=$(read_managed_schema "$scratch") || fail '无法核对已恢复的迁移状态'
  [[ "$result" == "$applied" ]] || fail '备份内容与开始时核对的迁移状态不一致'
  rehearse_migrations "$scratch" "$migration_dir" "$pending" 2>"$backup/rehearsal.log" ||
    fail "隔离迁移演练失败，见受限目录：$backup"
  printf '数据库备份及隔离迁移演练通过：%s\n' "$backup"
)
