#!/usr/bin/env bash
# Artemis installer. Design reference: https://github.com/mhtsec/ARTEX/blob/main/install.sh
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '[Artemis] %s\n' "$*"; }
die() { printf '[Artemis] %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'EOF'
用法：bash install.sh [选项]
  --mode docker|local       容器部署（默认）或本地编译
  --database existing|docker 本地模式连接已有 PostgreSQL 或启动容器数据库
  --build                   容器模式从源码构建，不拉取 Artemis 镜像
  --yes                     使用默认选项，不交互；已有数据库参数从环境变量读取
  --no-start                准备配置和构建/拉取镜像，不启动服务
  --install-docker          Linux 缺少 Docker 时使用官方脚本安装
  --tools baseline|名称列表  下载基线八项工具，或用逗号分隔选择；需 Python 3.8+，更新前先停止 Artemis
  --help                    显示帮助

Docker 需要 Compose >= 2.20；本地编译需要 go.mod 指定的 Go、Node.js 22、npm、rsync、Python 3。
数据库密码自动随机生成并保存到 .env（权限 600）；已有配置不会覆盖。
本地数据库参数：POSTGRES_HOST/PORT/USER/PASSWORD/DB/SSLMODE。
ARTEX_PG_DSN、ARTEX_CONFIG 保持应用原有优先级；模型在 UI 中配置。
EOF
}

mode='' database='' yes=0 source_build=0 no_start=0 install_docker=0 tools_selection='' tools_platform=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --mode|--database|--tools)
      [ "$#" -ge 2 ] || die "$1 缺少参数"
      case "$1" in
        --mode) mode=$2 ;;
        --database) database=$2 ;;
        --tools) [ -n "$2" ] || die '--tools 不能为空'; tools_selection=$2 ;;
      esac
      shift 2 ;;
    --build) source_build=1; shift ;;
    --yes|-y) yes=1; shift ;;
    --no-start) no_start=1; shift ;;
    --install-docker) install_docker=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

ask() {
  local answer
  if [ "$yes" = 1 ] || [ ! -t 0 ]; then printf '%s' "$2"; return; fi
  read -r -p "$1 [$2]: " answer || die '输入已关闭'
  printf '%s' "${answer:-$2}"
}
secret() {
  local answer
  if [ "$yes" = 1 ] || [ ! -t 0 ]; then printf '%s' "$2"; return; fi
  read -r -s -p "$1（输入隐藏，回车沿用或随机生成）: " answer || die '输入已关闭'
  printf '\n' >&2
  printf '%s' "${answer:-$2}"
}
random_password() { od -An -N24 -tx1 /dev/urandom | tr -d ' \n'; }
write_env_value() {
  local value=$2
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//\$/\$\$}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '%s="%s"\n' "$1" "$value"
}
need() { command -v "$1" >/dev/null 2>&1 || die "未检测到 $1，请安装后重试"; }
temp_files=()
cleanup() {
  if [ "${#temp_files[@]}" -gt 0 ]; then rm -f -- "${temp_files[@]}"; fi
}
trap cleanup EXIT
umask 077

ensure_docker() {
  if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    if [ "$install_docker" = 0 ]; then
      [ "$(ask '缺少 Docker/Compose，是否使用官方脚本安装？y/n' n)" = y ] \
        || die '请先安装 Docker 和 Compose 插件；Linux 可显式使用 --install-docker'
    fi
    [ "$(uname -s)" = Linux ] || die 'macOS/Windows 请先安装并启动 Docker Desktop'
    need curl
    local installer
    installer=$(mktemp)
    temp_files+=("$installer")
    curl --fail --show-error --location --connect-timeout 15 --max-time 120 \
      https://get.docker.com -o "$installer"
    if [ "$(id -u)" = 0 ]; then sh "$installer"; else need sudo; sudo sh "$installer"; fi
  fi
  docker info >/dev/null 2>&1 || die 'Docker daemon 不可访问；请启动 Docker 并确认当前用户有权限，然后重新执行脚本'
  docker compose up --help | grep -- '--wait-timeout' >/dev/null \
    || die '需要 Docker Compose >= 2.20（支持 up --wait-timeout）'
}

ensure_env() {
  if [ -f .env ]; then info '沿用已有 .env'; return; fi
  local generated line password
  generated=$(mktemp .env.XXXXXX)
  temp_files+=("$generated")
  # Persist effective first-install settings, so subsequent Compose runs use
  # the same credentials even after the caller's environment goes away.
  password=$(random_password)
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      POSTGRES_PASSWORD=*) write_env_value POSTGRES_PASSWORD "${POSTGRES_PASSWORD:-$password}" ;;
      POSTGRES_USER=*) write_env_value POSTGRES_USER "${POSTGRES_USER:-artemis}" ;;
      POSTGRES_DB=*) write_env_value POSTGRES_DB "${POSTGRES_DB:-artemis}" ;;
      ARTEX_IMAGE=*) write_env_value ARTEX_IMAGE "${ARTEX_IMAGE:-ghcr.io/x0rz4/artemis}" ;;
      ARTEX_TAG=*) write_env_value ARTEX_TAG "${ARTEX_TAG:-latest}" ;;
      ARTEX_BIND=*) write_env_value ARTEX_BIND "${ARTEX_BIND:-127.0.0.1}" ;;
      ARTEX_PORT=*) write_env_value ARTEX_PORT "${ARTEX_PORT:-8787}" ;;
      ARTEX_PROXY_PORT=*) write_env_value ARTEX_PROXY_PORT "${ARTEX_PROXY_PORT:-8788}" ;;
      ARTEX_PG_PORT=*) write_env_value ARTEX_PG_PORT "${ARTEX_PG_PORT:-5433}" ;;
      *) printf '%s\n' "$line" ;;
    esac
  done < .env.example > "$generated"
  mv "$generated" .env
  info '已生成 .env 和数据库配置（未提供密码时随机生成，未打印凭据）'
}

compose() { docker compose --env-file .env -f docker-compose.yml "$@"; }
local_compose() {
  docker compose --env-file .env -f docker-compose.yml -f docker-compose.local.yml "$@"
}

ensure_python() {
  if command -v python3 >/dev/null 2>&1; then PYTHON=python3; else need python; PYTHON=python; fi
  "$PYTHON" -c 'import sys; assert sys.version_info >= (3, 8), "需要 Python >= 3.8"'
}

prepare_tools() {
  [ -n "$tools_selection" ] || return 0
  ensure_python
  tools_platform=$1
  "$PYTHON" scripts/install_tools.py --tools "$tools_selection" --platform "$tools_platform" --check
}

install_tools() {
  [ -n "$tools_selection" ] || return 0
  "$PYTHON" scripts/install_tools.py --tools "$tools_selection" --platform "$tools_platform" --output "$PWD/data/tools"
  info '工具已准备；启动后在「系统 → 工具库」导入，再设置 Agent 绑定并启用。'
}

install_containers() {
  ensure_docker
  ensure_env
  compose config --quiet
  if [ -n "$tools_selection" ]; then
    local running daemon_platform
    running=$(compose ps --status running --services)
    if printf '%s\n' "$running" | grep -qx artemis; then
      die '安装工具前请先执行 docker compose stop artemis，避免与工具库写入冲突'
    fi
    daemon_platform=$(docker info --format '{{.OSType}}/{{.Architecture}}')
    [ -n "$daemon_platform" ] || die '无法确定 Docker daemon 平台'
    prepare_tools "$daemon_platform"
  fi
  if [ "$source_build" = 0 ]; then
    if ! compose pull artemis; then
      info '镜像拉取失败：请检查网络、标签及 GHCR 包的可见性。'
      [ "$(ask '是否改为从当前源码构建？y/n' y)" = y ] \
        || die '未启动服务；修复镜像配置后重试，或使用 --build'
      source_build=1
    fi
  fi
  if [ "$source_build" = 1 ]; then compose build artemis; fi
  compose pull postgres
  install_tools
  if [ "$no_start" = 1 ]; then info '配置与镜像已准备，未启动服务'; return; fi
  # No implicit build/pull here: failures above must not masquerade as success.
  if ! compose up -d --no-build --pull never --wait --wait-timeout 180; then
    compose ps
    die '服务未通过健康检查；请执行 docker compose logs --tail=100 查看原因'
  fi
  info '服务已通过健康检查；访问地址见下方 8787 的端口映射。默认 http://127.0.0.1:8787'
  compose port artemis 8787
  info '首次打开 /setup 设置管理员密码，然后在「系统 → LLM」配置模型。'
  info '日志：docker compose logs -f artemis；停止：docker compose down（保留数据卷）'
}

local_prerequisites() {
  need go; need node; need npm; need rsync
  ensure_python
  local required current
  required=$(awk '$1 == "go" {print $2; exit}' go.mod)
  current=$(go env GOVERSION)
  "$PYTHON" - "$required" "$current" <<'PY'
import re, sys
def version(text):
    match = re.search(r'(\d+)\.(\d+)(?:\.(\d+))?', text)
    if not match:
        raise SystemExit('无法识别 Go 版本: ' + text)
    return tuple(int(part or 0) for part in match.groups())
if version(sys.argv[2]) < version(sys.argv[1]):
    raise SystemExit('需要 Go >= ' + sys.argv[1])
PY
  node -e 'if (Number(process.versions.node.split(".")[0]) !== 22) { console.error("需要 Node.js 22"); process.exit(1); }'
}

configure_local_database() {
  if [ -n "${ARTEX_PG_DSN:-}" ]; then info '沿用 ARTEX_PG_DSN'; return; fi
  if [ -n "${ARTEX_CONFIG:-}" ]; then
    [ -f "$ARTEX_CONFIG" ] || die 'ARTEX_CONFIG 指定的文件不存在'
    info '沿用 ARTEX_CONFIG 指定的配置'; return
  fi
  if [ -f config.json ]; then
    info '沿用已有 config.json'
    if [ "$database" = docker ]; then prepare_local_postgres; fi
    return
  fi
  database=${database:-$(ask '数据库方式 existing/docker' existing)}
  case "$database" in
    docker)
      prepare_local_postgres
      ;;
    existing)
      local db_host db_port db_user db_password db_name db_ssl
      db_host=$(ask 'PostgreSQL 地址' "${POSTGRES_HOST:-127.0.0.1}")
      db_port=$(ask 'PostgreSQL 端口' "${POSTGRES_PORT:-5433}")
      db_user=$(ask 'PostgreSQL 用户' "${POSTGRES_USER:-artemis}")
      db_password=$(secret 'PostgreSQL 密码' "${POSTGRES_PASSWORD:-}")
      [ -n "$db_password" ] || die '已有数据库需要密码；可通过 POSTGRES_PASSWORD 提供'
      db_name=$(ask 'PostgreSQL 数据库名' "${POSTGRES_DB:-artemis}")
      db_ssl=$(ask 'sslmode' "${POSTGRES_SSLMODE:-disable}")
      POSTGRES_HOST="$db_host" POSTGRES_PORT="$db_port" POSTGRES_USER="$db_user" \
        POSTGRES_PASSWORD="$db_password" POSTGRES_DB="$db_name" POSTGRES_SSLMODE="$db_ssl" \
        "$PYTHON" scripts/install_config.py
      ;;
    *) die '数据库方式必须是 existing 或 docker' ;;
  esac
  info '已生成 config.json（权限 600）'
}

prepare_local_postgres() {
  ensure_docker
  ensure_env
  local_compose config --quiet
  if [ ! -f config.json ]; then
    local_compose config --format json | "$PYTHON" scripts/install_config.py --compose
  fi
  if [ "$no_start" = 0 ]; then
    local_compose pull postgres
    local_compose up -d --no-build --pull never --wait --wait-timeout 120 postgres
  fi
}

install_local() {
  case "$(uname -s)" in Linux|Darwin) ;; *) die 'Windows 请使用 build.bat 和 start.bat，或在 WSL 中运行本脚本' ;; esac
  local_prerequisites
  prepare_tools "$(go env GOOS)/$(go env GOARCH)"
  configure_local_database
  info '编译前端与内嵌二进制…'
  ARTEX_OUTPUT="$PWD/artemis" ARTEX_RELEASE=0 ARTEX_PACKAGE=0 ARTEX_SKIP_FRONTEND=0 \
    ARTEX_SKIP_NPM_CI=0 ARTEX_COMPRESS=off bash ./build.sh --target "$(go env GOOS)/$(go env GOARCH)"
  install_tools
  if [ "$no_start" = 1 ]; then info '构建完成，运行 sh start.sh -addr 127.0.0.1:8787 启动'; return; fi
  info '构建完成，前台启动 Artemis；Ctrl-C 停止。首次访问 /setup 设置管理员密码。'
  cleanup
  exec sh ./start.sh -addr "${ARTEX_BIND:-127.0.0.1}:${ARTEX_PORT:-8787}" \
    -proxy "127.0.0.1:${ARTEX_PROXY_PORT:-8788}"
}

mode=${mode:-$(ask '安装方式 docker/local' docker)}
case "$database" in ''|existing|docker) ;; *) die '数据库方式必须是 existing 或 docker' ;; esac
case "$mode" in
  docker)
    [ -z "$database" ] || die '--database 仅适用于 --mode local'
    install_containers ;;
  local)
    [ "$source_build" = 0 ] || die '--build 仅适用于容器模式；local 始终从源码编译'
    install_local ;;
  *) die '安装方式必须是 docker 或 local' ;;
esac
