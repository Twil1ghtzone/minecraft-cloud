#!/usr/bin/env bash
#
# AetherNet — interactive cluster installer.
#
# State-driven: every answer is written to /etc/aethernet/cluster.state
# so re-running this script picks up where it left off (or repairs a
# broken install) without re-prompting.
#
# Tested on: Debian 12, Ubuntu 22.04/24.04.

set -Eeuo pipefail

# ─── colours ─────────────────────────────────────────────────────────────────
readonly C_RESET="\033[0m"
readonly C_BOLD="\033[1m"
readonly C_DIM="\033[2m"
readonly C_RED="\033[0;31m"
readonly C_GREEN="\033[0;32m"
readonly C_YELLOW="\033[0;33m"
readonly C_BLUE="\033[0;34m"
readonly C_MAGENTA="\033[0;35m"
readonly C_CYAN="\033[0;36m"

readonly STATE_DIR="/etc/aethernet"
readonly STATE_FILE="${STATE_DIR}/cluster.state"
readonly DATA_DIR="/var/lib/aethernet"
readonly LOG_FILE="/var/log/aethernet-install.log"

# ─── ui helpers ──────────────────────────────────────────────────────────────
banner() {
  echo -e "${C_CYAN}"
  cat <<'EOF'
  ╔═══════════════════════════════════════════════════════════════════╗
  ║                                                                   ║
  ║                A E T H E R N E T   I N S T A L L E R              ║
  ║       decentralized minecraft cloud — multi-master cluster        ║
  ║                                                                   ║
  ╚═══════════════════════════════════════════════════════════════════╝
EOF
  echo -e "${C_RESET}"
}

hr()       { printf "${C_DIM}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${C_RESET}\n"; }
say()      { printf "${C_BLUE}▸ %s${C_RESET}\n" "$*"; }
ok()       { printf "${C_GREEN}✓ %s${C_RESET}\n" "$*"; }
warn()     { printf "${C_YELLOW}⚠ %s${C_RESET}\n" "$*" >&2; }
fail()     { printf "${C_RED}✗ %s${C_RESET}\n" "$*" >&2; exit 1; }
prompt()   { printf "${C_MAGENTA}? %s${C_RESET}" "$*"; }
step()     { printf "\n${C_BOLD}${C_CYAN}== %s ==${C_RESET}\n" "$*"; }

spinner_pid=""
start_spinner() {
  local msg="$1"
  ( while :; do for c in ⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏; do
      printf "\r${C_CYAN}%s${C_RESET} %s" "$c" "$msg"; sleep 0.08;
    done; done ) &
  spinner_pid=$!
  disown
}
stop_spinner() {
  [[ -n "$spinner_pid" ]] && kill "$spinner_pid" 2>/dev/null || true
  spinner_pid=""
  printf "\r\033[2K"
}

run() {
  # Run a command, append all output to log, drive a spinner.
  local msg="$1"; shift
  start_spinner "$msg"
  if "$@" >>"$LOG_FILE" 2>&1; then
    stop_spinner
    ok "$msg"
  else
    stop_spinner
    fail "$msg (see $LOG_FILE)"
  fi
}

# ─── state machine ───────────────────────────────────────────────────────────
declare -A STATE
state_load() {
  [[ -f "$STATE_FILE" ]] || return 0
  while IFS='=' read -r k v; do
    [[ -n "$k" && "$k" != \#* ]] && STATE[$k]="$v"
  done <"$STATE_FILE"
}
state_save() {
  mkdir -p "$STATE_DIR"
  : >"$STATE_FILE.tmp"
  for k in "${!STATE[@]}"; do
    printf '%s=%s\n' "$k" "${STATE[$k]}" >>"$STATE_FILE.tmp"
  done
  mv "$STATE_FILE.tmp" "$STATE_FILE"
  chmod 0600 "$STATE_FILE"
}
state_set() { STATE[$1]="$2"; state_save; }
state_get() { echo "${STATE[$1]:-}"; }
state_done() { [[ "${STATE[step_$1]:-}" == "done" ]]; }
state_mark() { state_set "step_$1" "done"; }

ask() {
  # ask <key> "<prompt>" [default]
  local key="$1" prmpt="$2" default="${3:-}"
  if [[ -n "${STATE[$key]:-}" ]]; then return; fi
  while :; do
    if [[ -n "$default" ]]; then
      prompt "$prmpt [${default}]: "
    else
      prompt "$prmpt: "
    fi
    read -r ans
    ans="${ans:-$default}"
    if [[ -n "$ans" ]]; then
      state_set "$key" "$ans"
      return
    fi
  done
}

ask_yn() {
  local key="$1" prmpt="$2" default="${3:-n}"
  if [[ -n "${STATE[$key]:-}" ]]; then return; fi
  while :; do
    prompt "$prmpt [y/n] (default: $default): "
    read -r ans
    ans="${ans:-$default}"
    case "${ans,,}" in
      y|yes) state_set "$key" "yes"; return;;
      n|no)  state_set "$key" "no";  return;;
    esac
  done
}

# ─── pre-flight ──────────────────────────────────────────────────────────────
preflight() {
  step "Pre-flight checks"
  [[ $EUID -eq 0 ]] || fail "Please run as root (sudo)."

  if ! command -v apt-get >/dev/null; then
    fail "This installer currently supports Debian/Ubuntu (apt-get not found)."
  fi

  local ram_mb
  ram_mb=$(awk '/MemTotal/ {printf("%d", $2/1024)}' /proc/meminfo)
  if (( ram_mb < 3500 )); then
    warn "System reports ${ram_mb} MiB RAM. AetherNet wants at least 4 GiB for a single node."
    ask_yn proceed_low_ram "Continue anyway?" n
    [[ "$(state_get proceed_low_ram)" == "yes" ]] || fail "Aborted by user."
  else
    ok "RAM: ${ram_mb} MiB"
  fi

  for port in 7000 7001 8080 2022 3306 6379; do
    if ss -lntp 2>/dev/null | awk '{print $4}' | grep -Eq ":${port}$"; then
      warn "Port ${port} is already in use."
      ask_yn "proceed_port_${port}" "Continue anyway?" n
      [[ "$(state_get proceed_port_${port})" == "yes" ]] || fail "Aborted by user."
    fi
  done
  ok "Required TCP ports inspected"

  if ! systemctl --version >/dev/null 2>&1; then
    fail "systemd not detected."
  fi
  ok "systemd present"
}

# ─── package install ─────────────────────────────────────────────────────────
install_packages() {
  state_done packages && { ok "packages already installed"; return; }
  step "Installing system packages"
  export DEBIAN_FRONTEND=noninteractive
  run "apt update" apt-get update -qq
  run "installing base tools" apt-get install -y -qq \
      curl ca-certificates gnupg ufw jq python3 \
      mariadb-server mariadb-galera-server \
      redis-server
  state_mark packages
}

install_docker() {
  state_done docker && { ok "docker already installed"; return; }
  step "Installing Docker Engine"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/debian/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  local codename
  codename=$(. /etc/os-release; echo "${VERSION_CODENAME}")
  cat >/etc/apt/sources.list.d/docker.list <<EOF
deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian ${codename} stable
EOF
  run "apt update (docker repo)" apt-get update -qq
  run "installing docker-ce" apt-get install -y -qq \
      docker-ce docker-ce-cli containerd.io docker-compose-plugin
  run "enabling docker" systemctl enable --now docker
  run "creating aethernet bridge network" docker network create --driver bridge aethernet || true
  state_mark docker
}

configure_galera() {
  state_done galera && { ok "galera already configured"; return; }
  step "Configuring MariaDB Galera"
  local cluster_name node_addrs
  cluster_name=$(state_get cluster_name)
  node_addrs=$(state_get galera_peers)
  cat >/etc/mysql/mariadb.conf.d/90-galera.cnf <<EOF
[galera]
wsrep_on=ON
wsrep_cluster_name="${cluster_name}"
wsrep_cluster_address="gcomm://${node_addrs}"
wsrep_provider=/usr/lib/galera/libgalera_smm.so
wsrep_sst_method=mariabackup
wsrep_node_address="$(state_get advertise_ip)"
wsrep_node_name="$(state_get node_id)"
binlog_format=ROW
default_storage_engine=InnoDB
innodb_autoinc_lock_mode=2
EOF
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    run "bootstrapping galera primary" galera_new_cluster
  else
    run "starting mariadb" systemctl restart mariadb
  fi
  state_mark galera
}

configure_redis() {
  state_done redis && { ok "redis already configured"; return; }
  step "Configuring Redis"
  sed -i 's/^bind 127.0.0.1.*/bind 0.0.0.0/' /etc/redis/redis.conf
  sed -i 's/^protected-mode yes/protected-mode no/' /etc/redis/redis.conf
  run "restarting redis" systemctl restart redis-server
  state_mark redis
}

configure_firewall() {
  state_done firewall && { ok "firewall already configured"; return; }
  step "Configuring UFW"
  if command -v ufw >/dev/null; then
    ufw allow ssh                 >>"$LOG_FILE" 2>&1 || true
    ufw allow 7000/tcp comment 'AetherNet Raft'   >>"$LOG_FILE" 2>&1 || true
    ufw allow 7001/tcp comment 'AetherNet gRPC'   >>"$LOG_FILE" 2>&1 || true
    ufw allow 8080/tcp comment 'AetherNet Panel'  >>"$LOG_FILE" 2>&1 || true
    ufw allow 2022/tcp comment 'AetherNet SFTP'   >>"$LOG_FILE" 2>&1 || true
    ufw allow 4567/tcp comment 'Galera SST'       >>"$LOG_FILE" 2>&1 || true
    ufw allow 4568/tcp comment 'Galera IST'       >>"$LOG_FILE" 2>&1 || true
    ufw allow 4444/tcp comment 'Galera mariabackup' >>"$LOG_FILE" 2>&1 || true
    yes | ufw enable              >>"$LOG_FILE" 2>&1 || true
    ok "UFW rules installed"
  else
    warn "ufw not present, skipping"
  fi
  state_mark firewall
}

install_daemon() {
  state_done daemon && { ok "aether-daemon already installed"; return; }
  step "Installing aether-daemon binaries"
  mkdir -p "$DATA_DIR" /etc/aethernet /var/log/aethernet
  if [[ -f /usr/local/bin/aether-daemon ]]; then
    ok "binary already present"
  else
    warn "aether-daemon binary not found in /usr/local/bin"
    warn "  build with 'make daemon' and copy to /usr/local/bin/aether-daemon"
  fi
  cat >/etc/aethernet/daemon.yaml <<EOF
node_id: $(state_get node_id)
data_dir: $DATA_DIR
raft_bind_addr: 0.0.0.0:7000
raft_advertise_addr: $(state_get advertise_ip):7000
grpc_listen: 0.0.0.0:7001
http_listen: 0.0.0.0:8080
sftp_listen: 0.0.0.0:2022

redis_addrs:
  - 127.0.0.1:6379

mariadb_addr: 127.0.0.1:3306
mariadb_user: aethernet
mariadb_pass: $(openssl rand -hex 16)

docker:
  endpoint: unix:///var/run/docker.sock
  scratch_path: $DATA_DIR/scratch
  template_path: $DATA_DIR/templates
  network_name: aethernet
EOF
  chmod 0600 /etc/aethernet/daemon.yaml

  cat >/etc/systemd/system/aether-daemon.service <<'UNIT'
[Unit]
Description=AetherNet daemon
After=network.target docker.service mariadb.service redis-server.service
Wants=docker.service mariadb.service redis-server.service

[Service]
ExecStart=/usr/local/bin/aether-daemon --config /etc/aethernet/daemon.yaml
Restart=on-failure
RestartSec=2s
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

  run "reloading systemd" systemctl daemon-reload
  run "enabling aether-daemon" systemctl enable aether-daemon
  state_mark daemon
}

bootstrap_or_join() {
  state_done cluster && { ok "cluster step already complete"; return; }
  step "Cluster bootstrap"
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    say "Bootstrapping new cluster on this node."
    run "starting aether-daemon (bootstrap)" systemctl start aether-daemon
  else
    say "Joining existing cluster at $(state_get join_addr)"
    /usr/local/bin/aether-daemon --config /etc/aethernet/daemon.yaml \
        --join "$(state_get join_addr)" \
        --join-token "$(state_get join_token)" &
    sleep 5
    run "starting aether-daemon" systemctl start aether-daemon
  fi
  state_mark cluster
}

# ─── flow ────────────────────────────────────────────────────────────────────
ask_questions() {
  step "Cluster configuration"
  ask cluster_name  "Cluster name" "aethernet"
  ask node_id       "Node id"      "$(hostname -s)"
  ask advertise_ip  "Advertise IP for this node" "$(hostname -I | awk '{print $1}')"
  ask_yn bootstrap  "Is this the FIRST node of a brand-new cluster?" n
  if [[ "$(state_get bootstrap)" == "no" ]]; then
    ask join_addr   "Join address of an existing node (host:7001)"
    ask join_token  "One-time join token"
    state_set galera_peers "$(state_get join_addr | cut -d: -f1)"
  else
    state_set galera_peers ""
  fi
}

main() {
  banner
  mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
  touch "$LOG_FILE"; chmod 0600 "$LOG_FILE"
  state_load
  preflight
  ask_questions
  install_packages
  install_docker
  configure_galera
  configure_redis
  configure_firewall
  install_daemon
  bootstrap_or_join
  hr
  ok  "AetherNet installation complete on node '$(state_get node_id)'."
  say "Web panel:   http://$(state_get advertise_ip):8080/"
  say "Cluster id:  $(state_get cluster_name)"
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    say "Run on the next node:"
    echo -e "    ${C_BOLD}sudo $0${C_RESET}"
    echo -e "  and answer ${C_BOLD}n${C_RESET} to the bootstrap question."
  fi
}

trap 'stop_spinner; echo; fail "interrupted"' INT TERM
main "$@"
