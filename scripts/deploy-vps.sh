#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Build and deploy M365-Copilot2API to the VPS.

Usage:
  ./scripts/deploy-vps.sh

Optional environment variables:
  SSH_TARGET       SSH host or alias (default: aws)
  REMOTE_DIR       Remote application directory
  SERVICE_NAME     systemd service name
  PUBLIC_BASE_URL  Public URL checked after deployment
  GO_BIN           Go executable to use
  DEPLOY_VERSION   Version label embedded in the binary
EOF
}

if [[ ${1:-} == "--help" || ${1:-} == "-h" ]]; then
  usage
  exit 0
fi
if [[ $# -ne 0 ]]; then
  usage >&2
  exit 2
fi

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ssh_target="${SSH_TARGET:-aws}"
remote_dir="${REMOTE_DIR:-/home/ubuntu/m365-copilot2api}"
service_name="${SERVICE_NAME:-m365-copilot2api.service}"
public_base_url="${PUBLIC_BASE_URL:-https://chatgpt.zeus.dpdns.org}"
public_base_url="${public_base_url%/}"

if [[ ! $remote_dir =~ ^/[A-Za-z0-9._/-]+$ || ! $service_name =~ ^[A-Za-z0-9_.@-]+$ ]]; then
  echo "REMOTE_DIR or SERVICE_NAME contains unsupported characters" >&2
  exit 2
fi

for command_name in ssh scp curl git sha256sum; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Missing required command: $command_name" >&2
    exit 1
  fi
done

go_bin="${GO_BIN:-$(command -v go || true)}"
if [[ -z $go_bin || ! -x $go_bin ]]; then
  echo "Go executable not found; set GO_BIN explicitly" >&2
  exit 1
fi

build_dir="$(mktemp -d /tmp/m365-deploy.XXXXXX)"
cleanup_local() {
  # Go's module cache is read-only by design; make it writable before removal.
  chmod -R u+w "$build_dir" 2>/dev/null || true
  rm -rf -- "$build_dir"
}
trap cleanup_local EXIT

deploy_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
commit="$(git -C "$repo_dir" rev-parse --short HEAD)"
dirty_suffix=""
if ! git -C "$repo_dir" diff --quiet || ! git -C "$repo_dir" diff --cached --quiet; then
  dirty_suffix="-local"
fi
version="${DEPLOY_VERSION:-${commit}${dirty_suffix}-${deploy_stamp}}"
artifact="$build_dir/m365-copilot2api"
remote_stage="$remote_dir/.deploy-${deploy_stamp}-$$"

cd "$repo_dir"

go_root=""
if ! go_root="$($go_bin env GOROOT 2>/dev/null)" || [[ ! -d $go_root ]]; then
  if command -v brew >/dev/null 2>&1; then
    go_root="$(brew --prefix go)/libexec"
  fi
fi
if [[ ! -d $go_root ]]; then
  echo "Unable to locate a valid GOROOT; set GO_BIN to a working Go installation" >&2
  exit 1
fi

echo "Building $version for linux/amd64..."
mkdir -p "$build_dir/gopath/pkg/mod"
env \
  GOROOT="$go_root" \
  GOPATH="$build_dir/gopath" \
  GOMODCACHE="$build_dir/gopath/pkg/mod" \
  GOOS=linux \
  GOARCH=amd64 \
  CGO_ENABLED=0 \
  "$go_bin" build -trimpath \
    -ldflags="-s -w -X m365-copilot2api/internal/web.Version=$version -X m365-copilot2api/internal/web.Commit=${commit}${dirty_suffix} -X m365-copilot2api/internal/web.BuildTime=$build_time" \
    -o "$artifact" ./cmd/server

echo "Checking current VPS deployment..."
ssh -o BatchMode=yes "$ssh_target" bash -s -- "$remote_dir" "$service_name" <<'REMOTE_PREFLIGHT'
set -eu
remote_dir=$1
service_name=$2
test -d "$remote_dir/web"
test -f "$remote_dir/m365-copilot2api"
test -f "$remote_dir/m365.env"
systemctl is-active --quiet "$service_name"
command -v curl >/dev/null
command -v python3 >/dev/null
REMOTE_PREFLIGHT

echo "Uploading binary and web files..."
ssh -o BatchMode=yes "$ssh_target" bash -s -- "$remote_stage" <<'REMOTE_STAGE'
set -eu
stage=$1
mkdir -m 700 "$stage"
REMOTE_STAGE
scp -p \
  "$artifact" \
  "$repo_dir/web/index.html" \
  "$repo_dir/web/login.html" \
  "$repo_dir/web/conversation.html" \
  "$repo_dir/web/debug.html" \
  "$ssh_target:$remote_stage/"

for file_name in m365-copilot2api index.html login.html conversation.html debug.html; do
  local_path="$artifact"
  if [[ $file_name != "m365-copilot2api" ]]; then
    local_path="$repo_dir/web/$file_name"
  fi
  local_sha="$(sha256sum "$local_path" | awk '{print $1}')"
  remote_sha="$(ssh -o BatchMode=yes "$ssh_target" sha256sum "$remote_stage/$file_name" | awk '{print $1}')"
  if [[ $local_sha != "$remote_sha" ]]; then
    echo "Checksum mismatch for $file_name" >&2
    exit 1
  fi
done

echo "Activating release..."
ssh -o BatchMode=yes "$ssh_target" bash -s -- \
  "$remote_dir" "$service_name" "$remote_stage" "$version" <<'REMOTE_DEPLOY'
set -Eeuo pipefail
remote_dir=$1
service_name=$2
stage=$3
expected_version=$4
web_dir="$remote_dir/web"
backup_root="$remote_dir/backups"
backup_stamp="$(date -u +%Y%m%dT%H%M%SZ)"
binary_backup="$backup_root/m365-copilot2api.$backup_stamp"
web_backup="$backup_root/web.$backup_stamp"
deployed=0
cookie_file=""

rollback_deploy() {
  result=$?
  if [[ $result -eq 0 ]]; then
    result=1
  fi
  trap - ERR HUP INT TERM
  if [[ -n $cookie_file ]]; then
    rm -f "$cookie_file"
  fi
  if [[ $deployed -eq 1 ]]; then
    echo "Deployment verification failed; restoring the previous release" >&2
    sudo systemctl stop "$service_name" || true
    install -m 755 "$binary_backup" "$remote_dir/.m365-copilot2api.rollback"
    mv -f "$remote_dir/.m365-copilot2api.rollback" "$remote_dir/m365-copilot2api"
    for file_name in index.html login.html conversation.html debug.html; do
      if [[ ! -f $web_backup/$file_name ]]; then
        continue
      fi
      install -m 644 "$web_backup/$file_name" "$web_dir/.$file_name.rollback"
      mv -f "$web_dir/.$file_name.rollback" "$web_dir/$file_name"
    done
    sudo systemctl start "$service_name" || true
  fi
  exit "$result"
}
trap rollback_deploy ERR HUP INT TERM

mkdir -p "$backup_root" "$web_backup"
cp -p "$remote_dir/m365-copilot2api" "$binary_backup"
cp -p "$web_dir/index.html" "$web_backup/index.html"
cp -p "$web_dir/login.html" "$web_backup/login.html"
if [[ -f $web_dir/conversation.html ]]; then
  cp -p "$web_dir/conversation.html" "$web_backup/conversation.html"
fi
cp -p "$web_dir/debug.html" "$web_backup/debug.html"

install -m 755 "$stage/m365-copilot2api" "$remote_dir/.m365-copilot2api.release"
install -m 644 "$stage/index.html" "$web_dir/.index.html.release"
install -m 644 "$stage/login.html" "$web_dir/.login.html.release"
install -m 644 "$stage/conversation.html" "$web_dir/.conversation.html.release"
install -m 644 "$stage/debug.html" "$web_dir/.debug.html.release"
deployed=1
mv -f "$web_dir/.debug.html.release" "$web_dir/debug.html"
mv -f "$web_dir/.conversation.html.release" "$web_dir/conversation.html"
mv -f "$web_dir/.login.html.release" "$web_dir/login.html"
mv -f "$web_dir/.index.html.release" "$web_dir/index.html"
mv -f "$remote_dir/.m365-copilot2api.release" "$remote_dir/m365-copilot2api"

sudo systemctl restart "$service_name"
ready=0
for _ in $(seq 1 20); do
  root_status="$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:4141/ || true)"
  if systemctl is-active --quiet "$service_name" && [[ $root_status == "200" ]]; then
    ready=1
    break
  fi
  sleep 1
done
[[ $ready -eq 1 ]]

set -a
. "$remote_dir/m365.env"
set +a
cookie_file="$(mktemp /tmp/m365-deploy-cookie.XXXXXX)"
login_payload="$(python3 -c 'import json,os; print(json.dumps({"password": os.environ["M365_ADMIN_PASSWORD"]}))')"
login_status="$(curl -sS -o /dev/null -w '%{http_code}' -c "$cookie_file" -H 'Content-Type: application/json' --data-binary "$login_payload" http://127.0.0.1:4141/api/admin/login)"
[[ $login_status == "200" ]]
version_json="$(curl -fsS -b "$cookie_file" http://127.0.0.1:4141/api/version)"
rm -f "$cookie_file"
cookie_file=""
[[ $version_json == *"\"version\":\"$expected_version\""* ]]

trap - ERR HUP INT TERM
rm -f "$stage/m365-copilot2api" "$stage/index.html" "$stage/login.html" "$stage/conversation.html" "$stage/debug.html"
rmdir "$stage"

echo "Service: $(systemctl is-active "$service_name")"
echo "Version: $expected_version"
echo "Binary backup: $binary_backup"
echo "Web backup: $web_backup"
REMOTE_DEPLOY

echo "Checking public endpoint..."
curl -fsS -o /dev/null "$public_base_url/"
echo "Deployment completed: $public_base_url/ ($version)"
