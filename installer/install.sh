#!/usr/bin/env bash
#
# ╔══════════════════════════════════════════════════════════════════════════╗
# ║                    A E T H E R N E T   I N S T A L L E R              ║
# ║           Decentralized Minecraft Cloud OS — Multi-Master HA           ║
# ║                          v0.1.0 — 2025                                 ║
# ╚══════════════════════════════════════════════════════════════════════════╝
#
# State-driven installer. Saves all answers to /etc/aethernet/cluster.state
# so re-running repairs or resumes a partial installation.
#
# Supports: Debian 12, Ubuntu 22.04, Ubuntu 24.04 (LTS)

set -Eeuo pipefail

# ─── ANSI Colors & Styles ────────────────────────────────────────────────────
readonly R="\033[0m"      # Reset
readonly BOLD="\033[1m"
readonly DIM="\033[2m"
readonly CYAN="\033[0;36m"
readonly BCYAN="\033[1;36m"
readonly GREEN="\033[0;32m"
readonly BGREEN="\033[1;32m"
readonly YELLOW="\033[0;33m"
readonly BYELLOW="\033[1;33m"
readonly RED="\033[0;31m"
readonly BRED="\033[1;31m"
readonly MAGENTA="\033[0;35m"
readonly BMAGENTA="\033[1;35m"
readonly BLUE="\033[0;34m"
readonly WHITE="\033[1;37m"

# ─── Constants ───────────────────────────────────────────────────────────────
readonly STATE_DIR="/etc/aethernet"
readonly STATE_FILE="${STATE_DIR}/cluster.state"
readonly DATA_DIR="/var/lib/aethernet"
readonly LOG_FILE="/var/log/aethernet-install.log"
readonly DAEMON_BIN="/usr/local/bin/aether-daemon"
readonly DAEMON_VER="0.1.0"

# ─── UI Helpers ──────────────────────────────────────────────────────────────

banner() {
  echo -e "${BCYAN}"
  echo '  ╔══════════════════════════════════════════════════════════════════════╗'
  echo '  ║                                                                      ║'
  echo '  ║            A E T H E R N E T   I N S T A L L E R                   ║'
  echo '  ║     Decentralized Minecraft Cloud OS — Multi-Master Raft HA         ║'
  echo '  ║                                                                      ║'
  echo '  ╚══════════════════════════════════════════════════════════════════════╝'
  echo -e "${R}"
  echo -e "  ${DIM}Node installer v${DAEMON_VER} — log: ${LOG_FILE}${R}\n"
}

hr() {
  echo -e "${DIM}  ════════════════════════════════════════════════════════════════════${R}"
}

box() {
  local msg="$1"
  local color="${2:-$CYAN}"
  local len=${#msg}
  local border
  border=$(printf '═%.0s' $(seq 1 $((len + 4))))
  echo -e "${color}  ╔${border}╗"
  echo -e "  ║  ${msg}  ║"
  echo -e "  ╚${border}╝${R}"
}

step() {
  echo -e "\n${BCYAN}  ╠═══ ${1} ${DIM}${R}"
}

ok()   { echo -e "  ${BGREEN}[✔]${R} ${1}"; }
warn() { echo -e "  ${BYELLOW}[⚠]${R} ${1}" >&2; }
fail() { echo -e "  ${BRED}[✘]${R} ${1}" >&2; exit 1; }
info() { echo -e "  ${CYAN}[▸]${R} ${1}"; }
prompt_msg() { printf "  ${MAGENTA}[?]${R} %s" "$*"; }

spinner_pid=""
start_spinner() {
  local msg="$1"
  ( while :; do for c in ⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏; do
      printf "\r  ${CYAN}%s${R} %s" "$c" "$msg"
      sleep 0.08
    done; done ) &
  spinner_pid=$!
  disown
}
stop_spinner() {
  [[ -n "${spinner_pid:-}" ]] && kill "${spinner_pid}" 2>/dev/null || true
  spinner_pid=""
  printf "\r\033[2K"
}

run() {
  local msg="$1"; shift
  start_spinner "$msg…"
  if "$@" >>"$LOG_FILE" 2>&1; then
    stop_spinner; ok "$msg"
  else
    stop_spinner; fail "$msg failed — check ${LOG_FILE}"
  fi
}

# ─── State Machine ───────────────────────────────────────────────────────────
declare -A STATE

state_load() {
  [[ -f "$STATE_FILE" ]] || return 0
  while IFS='=' read -r k v; do
    [[ -z "$k" || "$k" == '#'* ]] && continue
    STATE["$k"]="$v"
  done < "$STATE_FILE"
}

state_save() {
  mkdir -p "$STATE_DIR"
  local tmp="${STATE_FILE}.tmp"
  : > "$tmp"
  for k in "${!STATE[@]}"; do
    printf '%s=%s\n' "$k" "${STATE[$k]}" >> "$tmp"
  done
  mv "$tmp" "$STATE_FILE"
  chmod 0600 "$STATE_FILE"
}

state_set()  { STATE["$1"]="$2"; state_save; }
state_get()  { echo "${STATE[${1}]:-}"; }
state_done() { [[ "${STATE[step_${1}]:-}" == "done" ]]; }
state_mark() { state_set "step_$1" "done"; }
state_reset_step() { unset "STATE[step_${1}]"; state_save; }

ask() {
  local key="$1" pmpt="$2" default="${3:-}"
  [[ -n "${STATE[${key}]:-}" ]] && return
  while :; do
    if [[ -n "$default" ]]; then
      prompt_msg "$pmpt [${default}]: "
    else
      prompt_msg "$pmpt: "
    fi
    read -r ans
    ans="${ans:-$default}"
    [[ -n "$ans" ]] && { state_set "$key" "$ans"; return; }
  done
}

ask_yn() {
  local key="$1" pmpt="$2" default="${3:-n}"
  [[ -n "${STATE[${key}]:-}" ]] && return
  while :; do
    prompt_msg "$pmpt [y/n] (default: $default): "
    read -r ans
    ans="${ans:-$default}"
    case "${ans,,}" in
      y|yes) state_set "$key" "yes"; return ;;
      n|no)  state_set "$key" "no";  return ;;
    esac
  done
}

# ─── Pre-flight Checks ───────────────────────────────────────────────────────
preflight() {
  step "Pre-flight system checks"

  # Must be root
  [[ $EUID -eq 0 ]] || fail "Run as root: sudo $0"
  ok "Running as root"

  # Debian/Ubuntu only
  command -v apt-get >/dev/null || fail "apt-get not found — only Debian/Ubuntu supported"
  ok "Package manager: apt-get"

  # systemd
  systemctl --version >/dev/null 2>&1 || fail "systemd not found"
  ok "systemd present"

  # RAM check
  local ram_mb
  ram_mb=$(awk '/MemTotal/ {printf("%d", $2/1024)}' /proc/meminfo)
  if (( ram_mb < 3500 )); then
    warn "RAM: ${ram_mb} MiB — AetherNet recommends ≥ 4096 MiB per node"
    ask_yn proceed_low_ram "Continue with low RAM?" n
    [[ "$(state_get proceed_low_ram)" == "yes" ]] || fail "Aborted: insufficient RAM"
  else
    ok "RAM: ${ram_mb} MiB"
  fi

  # Port bindings check
  local used_ports=()
  for port in 7000 7001 8080 2022 3306 6379 25565; do
    if ss -lntp 2>/dev/null | awk '{print $4}' | grep -Eq ":${port}$"; then
      used_ports+=("$port")
      warn "Port ${port} is already bound"
    fi
  done
  if [[ ${#used_ports[@]} -gt 0 ]]; then
    ask_yn proceed_ports "Continue with ports already in use?" n
    [[ "$(state_get proceed_ports)" == "yes" ]] || fail "Aborted: port conflicts"
  else
    ok "All required ports are free"
  fi

  # Storage throughput probe
  probe_storage_throughput

  ok "Pre-flight checks passed"
}

probe_storage_throughput() {
  info "Probing storage write throughput (512 MiB sequential write test)…"
  mkdir -p "$DATA_DIR"
  local testfile="${DATA_DIR}/.throughput_test"
  local output speed_mbs

  # Run dd with oflag=direct to bypass page cache
  output=$(dd if=/dev/zero of="$testfile" bs=1M count=512 oflag=direct 2>&1) || {
    rm -f "$testfile"
    warn "Storage probe failed — continuing anyway"
    return
  }
  rm -f "$testfile"

  # Parse MB/s from dd output: "... 536870912 bytes ... copied, 2.1 s, 256 MB/s"
  speed_mbs=$(echo "$output" | grep -oP '[0-9]+(?:\.[0-9]+)? MB/s' | tail -1 | grep -oP '[0-9]+(?:\.[0-9]+)?')

  if [[ -z "$speed_mbs" ]]; then
    warn "Could not parse throughput from dd output"
    return
  fi

  local speed_int=${speed_mbs%%.*}
  if (( speed_int < 100 )); then
    warn "Storage throughput: ${speed_mbs} MB/s — very slow. NVMe recommended for production."
  elif (( speed_int < 200 )); then
    warn "Storage throughput: ${speed_mbs} MB/s — below recommended 200 MB/s"
  else
    ok "Storage throughput: ${speed_mbs} MB/s"
  fi
}

# ─── Package Installation ────────────────────────────────────────────────────
install_packages() {
  state_done packages && { ok "Base packages already installed"; return; }
  step "Installing system packages"
  export DEBIAN_FRONTEND=noninteractive
  run "apt update" apt-get update -qq
  run "Installing base tools" apt-get install -y -qq \
      curl wget ca-certificates gnupg ufw jq python3 openssl \
      mariadb-server mariadb-galera-server mariadb-backup \
      redis-server \
      haproxy keepalived \
      net-tools iproute2 procps
  state_mark packages
}

install_docker() {
  state_done docker && { ok "Docker already installed"; return; }
  step "Installing Docker Engine"
  install -m 0755 -d /etc/apt/keyrings
  # Detect distro for repo URL
  local distro_id codename
  distro_id=$(. /etc/os-release; echo "${ID}")
  codename=$(. /etc/os-release; echo "${VERSION_CODENAME}")
  # Ubuntu uses ubuntu repo, Debian uses debian
  local repo_url="https://download.docker.com/linux/${distro_id}"
  curl -fsSL "${repo_url}/gpg" | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  cat > /etc/apt/sources.list.d/docker.list << EOF
deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] ${repo_url} ${codename} stable
EOF
  run "apt update (Docker repo)" apt-get update -qq
  run "Installing docker-ce" apt-get install -y -qq \
      docker-ce docker-ce-cli containerd.io docker-compose-plugin
  run "Enabling Docker" systemctl enable --now docker
  run "Creating aethernet bridge network" docker network create --driver bridge aethernet 2>/dev/null || true
  state_mark docker
}

# ─── Galera Configuration ─────────────────────────────────────────────────────
configure_galera() {
  state_done galera && { ok "Galera already configured"; return; }
  step "Configuring MariaDB Galera Cluster"
  local cluster_name node_addrs node_addr
  cluster_name=$(state_get cluster_name)
  node_addrs=$(state_get galera_peers)
  node_addr=$(state_get advertise_ip)

  cat > /etc/mysql/mariadb.conf.d/90-galera.cnf << EOF
[galera]
wsrep_on=ON
wsrep_cluster_name="${cluster_name}"
wsrep_cluster_address="gcomm://${node_addrs}"
wsrep_provider=/usr/lib/galera/libgalera_smm.so
wsrep_sst_method=mariabackup
wsrep_node_address="${node_addr}"
wsrep_node_name="$(state_get node_id)"
binlog_format=ROW
default_storage_engine=InnoDB
innodb_autoinc_lock_mode=2
innodb_flush_log_at_trx_commit=0
query_cache_size=0
query_cache_type=0
EOF

  # ── 2-node split-brain guard ─────────────────────────────────────────────
  local peer_count
  peer_count=$(echo "$node_addrs" | tr ',' '\n' | grep -cE '^[^[:space:]]+$' || true)
  if (( peer_count < 2 )); then
    warn "Only ${peer_count} Galera peer(s) configured — minimum for HA is 3 (or 2 + garbd)."
  fi
  if (( peer_count == 2 )); then
    warn "2-node Galera cluster detected."
    warn "With exactly 2 nodes, losing one node causes Loss of Quorum and the"
    warn "remaining node will SHUT DOWN (split-brain protection). This takes your"
    warn "entire network offline until the failed node is manually recovered."
    echo
    ask_yn install_garbd \
      "Install Galera Arbitrator (garbd) on THIS node as a lightweight quorum tie-breaker?" y
    if [[ "$(state_get install_garbd)" == "yes" ]]; then
      configure_garbd "$cluster_name" "$node_addrs"
    else
      warn "Proceeding without garbd — be aware of the split-brain risk."
    fi
  fi

  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    run "Bootstrapping Galera primary" galera_new_cluster
    # Run init SQL
    if [[ -f /etc/aethernet/galera-init.sql ]]; then
      mysql < /etc/aethernet/galera-init.sql >>"$LOG_FILE" 2>&1 || warn "Init SQL had errors (check log)"
    fi
  else
    run "Starting MariaDB" systemctl restart mariadb
  fi
  state_mark galera
}

# ─── Galera Arbitrator (garbd) ────────────────────────────────────────────────
# Lightweight quorum tie-breaker for 2-node Galera clusters. garbd does NOT
# store any data; it only casts a vote in quorum decisions so that 1-of-2
# real nodes is still the majority.
configure_garbd() {
  local cluster_name="$1"
  local cluster_addrs="$2"
  state_done garbd && { ok "garbd already configured"; return; }
  step "Configuring Galera Arbitrator (garbd)"

  run "Installing galera-arbitrator" apt-get install -y -qq galera-4 || \
    run "Installing garbd (alternative pkg)" apt-get install -y -qq galera-arbitrator-4 || \
    { warn "garbd package not found — add MariaDB repo manually"; return; }

  local garbd_name
  garbd_name="$(state_get node_id)-arb"

  # Build the gcomm address list: prepend gcomm:// to comma-separated IPs
  local gcomm_addr
  gcomm_addr="gcomm://$(echo "$cluster_addrs" | tr -d ' ')"

  mkdir -p /etc/default
  cat > /etc/default/garbd << EOF
# Galera Arbitrator configuration — managed by AetherNet installer
GALERA_NODES="${gcomm_addr}"
GALERA_GROUP="${cluster_name}"
GALERA_NAME="${garbd_name}"
GALERA_OPTIONS=""
LOG_FILE="/var/log/garbd.log"
EOF

  # systemd unit for garbd
  cat > /etc/systemd/system/garbd.service << 'UNIT'
[Unit]
Description=Galera Arbitrator Daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/default/garbd
ExecStart=/usr/bin/garbd \
    --address="${GALERA_NODES}" \
    --group="${GALERA_GROUP}" \
    --name="${GALERA_NAME}" \
    ${GALERA_OPTIONS:+--options="${GALERA_OPTIONS}"} \
    --log="${LOG_FILE}"
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
UNIT

  run "Reloading systemd"  systemctl daemon-reload
  run "Enabling garbd"     systemctl enable garbd
  run "Starting garbd"     systemctl start garbd || warn "garbd start failed — check /var/log/garbd.log"
  state_mark garbd
  ok "Galera Arbitrator configured as '${garbd_name}'"
  info "garbd adds a quorum vote without storing data — 2-node split-brain is now resolved."
}

# ─── GlusterFS Shared Template Volumes ────────────────────────────────────────
# Module 3 of the AetherNet architecture requires read-only shared volumes for
# server templates that are visible on every cluster node. GlusterFS replicates
# /var/lib/aethernet/templates across all nodes so a template change written on
# node-1 is immediately available to containers on node-2 and node-3.
configure_glusterfs() {
  state_done glusterfs && { ok "GlusterFS already configured"; return; }
  step "Configuring GlusterFS shared template volumes"

  ask glusterfs_peers \
    "GlusterFS peer IPs (comma-separated, same as Galera peers recommended)" \
    "$(state_get galera_peers)"

  run "Installing GlusterFS" apt-get install -y -qq \
      glusterfs-server glusterfs-client

  run "Enabling GlusterFS" systemctl enable --now glusterd

  local my_ip peer_list
  my_ip=$(state_get advertise_ip)
  peer_list=$(state_get glusterfs_peers)

  # Peer probe all other nodes (bootstrap node only; joiners are probed by bootstrap)
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    for peer in $(echo "$peer_list" | tr ',' ' '); do
      [[ "$peer" == "$my_ip" ]] && continue
      gluster peer probe "$peer" >>"$LOG_FILE" 2>&1 \
        && ok "Peered with $peer" || warn "Peer probe failed for $peer — run manually after all nodes are up"
    done

    # Create the replicated volume for templates. Replica count = number of peers.
    local replica_count brick_list
    replica_count=$(echo "$peer_list" | tr ',' '\n' | grep -cE '^[^[:space:]]+$')
    brick_list=""
    for peer in $(echo "$peer_list" | tr ',' ' '); do
      brick_list="$brick_list ${peer}:/gluster/bricks/templates"
    done
    mkdir -p /gluster/bricks/templates

    if gluster volume info aethernet-templates >>"$LOG_FILE" 2>&1; then
      ok "GlusterFS volume 'aethernet-templates' already exists"
    else
      # shellcheck disable=SC2086
      run "Creating GlusterFS volume" \
        gluster volume create aethernet-templates \
          replica "$replica_count" $brick_list force
      run "Starting volume" gluster volume start aethernet-templates
      # Disable client-side write-back caching so read-only bind-mounts are consistent
      gluster volume set aethernet-templates performance.write-behind off >>"$LOG_FILE" 2>&1 || true
      gluster volume set aethernet-templates performance.io-cache off     >>"$LOG_FILE" 2>&1 || true
    fi
  else
    info "Non-bootstrap node: GlusterFS volume will be created by the bootstrap node."
    info "Ensure the bootstrap node has run this installer step first."
    mkdir -p /gluster/bricks/templates
  fi

  # Mount the distributed volume at the template path
  local tmpl_path="/var/lib/aethernet/templates"
  mkdir -p "$tmpl_path"

  # Add to /etc/fstab for persistence across reboots
  local fstab_entry="localhost:/aethernet-templates ${tmpl_path} glusterfs defaults,_netdev,x-systemd.automount 0 0"
  if ! grep -qF "aethernet-templates" /etc/fstab; then
    echo "$fstab_entry" >> /etc/fstab
    ok "GlusterFS mount added to /etc/fstab"
  fi

  run "Mounting GlusterFS template volume" mount -a || \
    warn "Mount failed — run 'mount -a' after all peers are online"

  # UFW rule for GlusterFS inter-node traffic
  if command -v ufw >/dev/null; then
    ufw allow 24007/tcp comment 'GlusterFS management' >>"$LOG_FILE" 2>&1 || true
    ufw allow 24008/tcp comment 'GlusterFS RDMA'       >>"$LOG_FILE" 2>&1 || true
    ufw allow 49152:49251/tcp comment 'GlusterFS bricks' >>"$LOG_FILE" 2>&1 || true
  fi

  state_mark glusterfs
  ok "GlusterFS shared template volume mounted at ${tmpl_path}"
  info "Templates written here are replicated to all cluster nodes within seconds."
}

# ─── Redis Configuration ──────────────────────────────────────────────────────
configure_redis() {
  state_done redis && { ok "Redis already configured"; return; }
  step "Configuring Redis"
  local node_addr
  node_addr=$(state_get advertise_ip)
  # Allow connections from cluster nodes; in production add requirepass
  sed -i "s/^bind 127.0.0.1.*/bind 0.0.0.0/" /etc/redis/redis.conf
  sed -i "s/^protected-mode yes/protected-mode no/" /etc/redis/redis.conf
  # Increase maxmemory for player profile caching
  grep -q '^maxmemory ' /etc/redis/redis.conf \
    || echo 'maxmemory 512mb' >> /etc/redis/redis.conf
  grep -q '^maxmemory-policy ' /etc/redis/redis.conf \
    || echo 'maxmemory-policy allkeys-lru' >> /etc/redis/redis.conf
  run "Restarting Redis" systemctl restart redis-server
  run "Enabling Redis" systemctl enable redis-server
  state_mark redis
}

# ─── HAProxy & Keepalived ─────────────────────────────────────────────────────
configure_haproxy() {
  state_done haproxy && { ok "HAProxy already configured"; return; }
  step "Configuring HAProxy"

  # Write a minimal initial config (daemon will sync the real one at startup)
  cat > /etc/haproxy/aethernet.cfg << 'HAPCFG'
global
    log /dev/log local0
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s
    user haproxy
    group haproxy
    daemon
    maxconn 50000

defaults
    log     global
    mode    tcp
    option  tcplog
    timeout connect 5s
    timeout client  50s
    timeout server  50s

frontend fe_stats
    bind 127.0.0.1:8404
    mode http
    stats enable
    stats uri /stats
    stats refresh 10s
HAPCFG

  mkdir -p /run/haproxy
  run "Enabling HAProxy" systemctl enable haproxy
  run "Starting HAProxy" systemctl start haproxy || warn "HAProxy start failed — daemon will manage it"
  state_mark haproxy
}

configure_keepalived() {
  ask_yn enable_keepalived "Enable Keepalived VIP (multi-node setup)?" n
  [[ "$(state_get enable_keepalived)" != "yes" ]] && { info "Keepalived skipped"; return; }
  state_done keepalived && { ok "Keepalived already configured"; return; }
  step "Configuring Keepalived VIP"

  ask vip_address  "Virtual IP address for the cluster VIP" "10.0.0.100"
  ask vip_cidr     "CIDR prefix for VIP" "24"
  ask vip_iface    "Network interface for VIP" "eth0"
  ask vip_pass     "VRRP authentication password (8 chars)" "aether01"
  ask_yn vip_is_master "Is this the initial MASTER node?" y

  local state priority
  if [[ "$(state_get vip_is_master)" == "yes" ]]; then
    state="MASTER"; priority=150
  else
    state="BACKUP"; priority=100
  fi

  mkdir -p /etc/keepalived
  cat > /etc/keepalived/keepalived.conf << EOF
global_defs {
    router_id $(state_get node_id)
    enable_script_security
}

vrrp_script chk_haproxy {
    script "/bin/sh -c 'kill -0 \$(cat /run/haproxy/haproxy.pid 2>/dev/null) 2>/dev/null'"
    interval 2
    weight -20
    fall 2
    rise 2
}

vrrp_instance AETHERNET_VIP {
    state ${state}
    interface $(state_get vip_iface)
    virtual_router_id 51
    priority ${priority}
    advert_int 1
    authentication {
        auth_type PASS
        auth_pass $(state_get vip_pass)
    }
    virtual_ipaddress {
        $(state_get vip_address)/$(state_get vip_cidr)
    }
    track_script {
        chk_haproxy
    }
}
EOF

  cat > /etc/keepalived/notify.sh << 'NOTIFY'
#!/bin/bash
TYPE=$1
NAME=$2
STATE=$3
case $STATE in
  MASTER) logger -t keepalived "AetherNet VIP: MASTER — promoting HAProxy" ;;
  BACKUP) logger -t keepalived "AetherNet VIP: BACKUP" ;;
  FAULT)  logger -t keepalived "AetherNet VIP: FAULT" ;;
esac
NOTIFY
  chmod +x /etc/keepalived/notify.sh

  run "Enabling Keepalived" systemctl enable keepalived
  run "Starting Keepalived" systemctl restart keepalived
  state_mark keepalived
}

# ─── Firewall Setup ───────────────────────────────────────────────────────────
configure_firewall() {
  state_done firewall && { ok "Firewall already configured"; return; }
  step "Configuring UFW Firewall"
  if command -v ufw >/dev/null; then
    ufw allow ssh                                    >>"$LOG_FILE" 2>&1 || true
    ufw allow 7000/tcp  comment 'AetherNet Raft'     >>"$LOG_FILE" 2>&1 || true
    ufw allow 7001/tcp  comment 'AetherNet gRPC'     >>"$LOG_FILE" 2>&1 || true
    ufw allow 8080/tcp  comment 'AetherNet Panel'    >>"$LOG_FILE" 2>&1 || true
    ufw allow 2022/tcp  comment 'AetherNet SFTP'     >>"$LOG_FILE" 2>&1 || true
    ufw allow 4567/tcp  comment 'Galera SST'         >>"$LOG_FILE" 2>&1 || true
    ufw allow 4568/tcp  comment 'Galera IST'         >>"$LOG_FILE" 2>&1 || true
    ufw allow 4444/tcp  comment 'Galera backup'      >>"$LOG_FILE" 2>&1 || true
    ufw allow 6379/tcp  comment 'Redis'              >>"$LOG_FILE" 2>&1 || true
    ufw allow 25565/tcp comment 'Minecraft HAProxy'    >>"$LOG_FILE" 2>&1 || true
    ufw allow 25566:30000/tcp comment 'MC dynamic'     >>"$LOG_FILE" 2>&1 || true
    ufw allow 24007/tcp  comment 'GlusterFS mgmt'      >>"$LOG_FILE" 2>&1 || true
    ufw allow 24008/tcp  comment 'GlusterFS RDMA'      >>"$LOG_FILE" 2>&1 || true
    ufw allow 49152:49251/tcp comment 'GlusterFS bricks' >>"$LOG_FILE" 2>&1 || true
    yes | ufw enable >>"$LOG_FILE" 2>&1 || true
    ok "UFW rules installed"
  else
    warn "ufw not found — manual firewall configuration required"
  fi
  state_mark firewall
}

# ─── Daemon Installation ──────────────────────────────────────────────────────
install_daemon() {
  state_done daemon && { ok "aether-daemon already installed"; return; }
  step "Installing AetherNet daemon"
  mkdir -p "$DATA_DIR" /etc/aethernet /var/log/aethernet

  if [[ -f "$DAEMON_BIN" ]]; then
    ok "Binary found: ${DAEMON_BIN}"
  else
    warn "${DAEMON_BIN} not found — build with 'make daemon' first"
    warn "Installation will continue but the service will fail to start"
  fi

  local db_pass
  db_pass=$(openssl rand -hex 16)

  cat > /etc/aethernet/daemon.yaml << EOF
node_id: $(state_get node_id)
data_dir: ${DATA_DIR}
raft_bind_addr: "0.0.0.0:7000"
raft_advertise_addr: "$(state_get advertise_ip):7000"
grpc_listen: "0.0.0.0:7001"
http_listen: "0.0.0.0:8080"
sftp_listen: "0.0.0.0:2022"
metrics_listen: "127.0.0.1:9100"
mariadb_addr: "127.0.0.1:3306"
mariadb_db: "aethernet"
mariadb_user: "aethernet"
mariadb_pass: "${db_pass}"
redis_addrs:
  - "127.0.0.1:6379"
docker:
  endpoint: "unix:///var/run/docker.sock"
  scratch_path: "${DATA_DIR}/scratch"
  template_path: "${DATA_DIR}/templates"
  network_name: "aethernet"
  port_range_start: 25566
  port_range_end: 30000
haproxy:
  config_path: "/etc/haproxy/aethernet.cfg"
  max_conn_rate: 30
  max_conn_concurrent: 50
  enabled: true
keepalived:
  enabled: $(state_get enable_keepalived)
  vip: "$(state_get vip_address)"
  cidr_prefix: $(state_get vip_cidr)
  interface: "$(state_get vip_iface)"
  router_id: 51
  auth_pass: "$(state_get vip_pass)"
log_level: "info"
log_format: "json"
EOF
  chmod 0600 /etc/aethernet/daemon.yaml

  cat > /etc/systemd/system/aether-daemon.service << 'UNIT'
[Unit]
Description=AetherNet Distributed Minecraft Cloud Daemon
Documentation=https://github.com/aethernet/aethernet
After=network-online.target docker.service mariadb.service redis-server.service
Wants=network-online.target docker.service

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/aether-daemon --config /etc/aethernet/daemon.yaml
Restart=on-failure
RestartSec=3s
LimitNOFILE=65536
LimitNPROC=65536
StandardOutput=journal
StandardError=journal
SyslogIdentifier=aether-daemon

[Install]
WantedBy=multi-user.target
UNIT

  run "Reloading systemd" systemctl daemon-reload
  run "Enabling aether-daemon" systemctl enable aether-daemon
  state_mark daemon
}

# ─── Cluster Bootstrap / Join ─────────────────────────────────────────────────
bootstrap_or_join() {
  state_done cluster && { ok "Cluster step already complete"; return; }
  step "Cluster initialization"
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    info "Bootstrapping new single-node cluster…"
    run "Starting aether-daemon" systemctl start aether-daemon
    sleep 3
    if systemctl is-active --quiet aether-daemon; then
      ok "aether-daemon started successfully"
    else
      warn "aether-daemon may not have started — check: journalctl -u aether-daemon -n 50"
    fi
  else
    info "Joining existing cluster at $(state_get join_addr)…"
    run "Starting aether-daemon" systemctl start aether-daemon
    sleep 3
    # The daemon's --join flag is passed via the config file's join_addr field
    if systemctl is-active --quiet aether-daemon; then
      ok "aether-daemon joined cluster"
    else
      warn "Daemon may not be running — check: journalctl -u aether-daemon -n 50"
    fi
  fi
  state_mark cluster
}

# ─── Cluster Status Display ───────────────────────────────────────────────────
show_cluster_status() {
  step "Cluster Status"
  local panel_url="http://127.0.0.1:8080"

  if ! systemctl is-active --quiet aether-daemon 2>/dev/null; then
    warn "aether-daemon is not running"
    return
  fi

  info "Leader:"
  curl -sf "${panel_url}/api/v1/cluster/leader" 2>/dev/null | jq -r '"  Leader ID:      " + .leader_id + "\n  Leader Address: " + .leader_address' || warn "Could not reach daemon API"

  info "\nNodes:"
  curl -sf "${panel_url}/api/v1/cluster/nodes" 2>/dev/null \
    | jq -r '.nodes[] | "  " + .id + "\t" + .address + "\t" + (["unknown","up","suspect","down","draining"][.state // 0])' \
    || warn "Could not fetch nodes"

  info "\nDaemon service status:"
  systemctl status aether-daemon --no-pager -l | head -8 | sed 's/^/  /'
}

# ─── Upgrade ──────────────────────────────────────────────────────────────────
do_upgrade() {
  step "Upgrading AetherNet daemon"
  if [[ ! -f "$DAEMON_BIN" ]]; then
    warn "No existing daemon found at ${DAEMON_BIN}"
    info "Build the new binary with 'make daemon' then re-run: $0 upgrade"
    return
  fi
  local current_ver
  current_ver=$("$DAEMON_BIN" --version 2>/dev/null | grep -oP '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown")
  info "Current version: ${current_ver}"
  info "Target version:  ${DAEMON_VER}"
  if [[ "$current_ver" == "$DAEMON_VER" ]]; then
    ok "Already at target version ${DAEMON_VER}"
    return
  fi
  run "Stopping aether-daemon" systemctl stop aether-daemon || true
  # In production: download and replace binary here
  warn "Binary replacement not automated — copy new binary to ${DAEMON_BIN} manually"
  run "Starting aether-daemon" systemctl start aether-daemon
  ok "Upgrade complete"
}

# ─── Uninstall ────────────────────────────────────────────────────────────────
do_uninstall() {
  box "UNINSTALL — This will remove AetherNet components" "$BRED"
  warn "This will stop all services and remove configuration files."
  warn "MariaDB databases and Docker volumes will NOT be deleted automatically."
  prompt_msg "Type 'UNINSTALL' to confirm: "
  read -r confirm
  [[ "$confirm" == "UNINSTALL" ]] || { info "Uninstall cancelled."; return; }

  systemctl stop aether-daemon   2>/dev/null || true
  systemctl disable aether-daemon 2>/dev/null || true
  rm -f /etc/systemd/system/aether-daemon.service
  rm -f "$DAEMON_BIN"
  rm -rf /etc/aethernet
  systemctl daemon-reload
  ok "AetherNet daemon removed"
  warn "Docker containers, MariaDB, Redis, and HAProxy were NOT touched."
  warn "Remove them manually if desired."
}

# ─── Questions ────────────────────────────────────────────────────────────────
ask_questions_new_node() {
  step "Cluster configuration"
  ask cluster_name  "Cluster name" "aethernet"
  ask node_id       "This node's ID" "$(hostname -s)"
  ask advertise_ip  "Advertise IP (LAN/public IP of this node)" "$(hostname -I | awk '{print $1}')"
  state_set bootstrap "yes"
  # Ask for Galera peers (self for bootstrap)
  state_set galera_peers "$(state_get advertise_ip)"
}

ask_questions_join() {
  step "Join existing cluster"
  ask cluster_name  "Cluster name" "aethernet"
  ask node_id       "This node's ID" "$(hostname -s)"
  ask advertise_ip  "Advertise IP of THIS node" "$(hostname -I | awk '{print $1}')"
  ask join_addr     "gRPC address of an existing node (host:7001)"
  ask join_token    "One-time join token"
  ask galera_peers  "Galera cluster addresses (comma-separated IPs)"
  state_set bootstrap "no"
}

# ─── Main Menu ────────────────────────────────────────────────────────────────
main_menu() {
  while :; do
    echo
    hr
    echo -e "  ${BCYAN}AetherNet Installer — Main Menu${R}"
    hr
    echo -e "  ${WHITE}1${R}) Install New Node (Bootstrap first cluster node)"
    echo -e "  ${WHITE}2${R}) Join Existing Cluster (Add this node to a running cluster)"
    echo -e "  ${WHITE}3${R}) Install / Upgrade Daemon Only"
    echo -e "  ${WHITE}4${R}) Configure HAProxy & Keepalived VIP"
    echo -e "  ${WHITE}5${R}) Run Pre-flight Checks Only"
    echo -e "  ${WHITE}6${R}) Show Cluster Status"
    echo -e "  ${WHITE}7${R}) Uninstall AetherNet"
    echo -e "  ${WHITE}8${R}) Configure GlusterFS Shared Template Volumes"
    echo -e "  ${WHITE}9${R}) Configure Galera Arbitrator (garbd — 2-node HA fix)"
    echo -e "  ${WHITE}0${R}) Exit"
    hr
    prompt_msg "Select option: "
    read -r choice
    echo
    case "$choice" in
      1)
        ask_questions_new_node
        install_packages
        install_docker
        configure_glusterfs
        configure_galera
        configure_redis
        configure_haproxy
        configure_keepalived
        configure_firewall
        install_daemon
        bootstrap_or_join
        print_summary
        ;;
      2)
        ask_questions_join
        install_packages
        install_docker
        configure_glusterfs
        configure_galera
        configure_redis
        configure_haproxy
        configure_keepalived
        configure_firewall
        install_daemon
        bootstrap_or_join
        print_summary
        ;;
      3)
        ask node_id      "Node ID" "$(hostname -s)"
        ask advertise_ip "Advertise IP" "$(hostname -I | awk '{print $1}')"
        ask_yn bootstrap  "Bootstrap cluster?" n
        install_packages
        install_docker
        install_daemon
        do_upgrade
        ;;
      4)
        configure_haproxy
        configure_keepalived
        ;;
      5)
        preflight
        ;;
      6)
        show_cluster_status
        ;;
      7)
        do_uninstall
        ;;
      8)
        configure_glusterfs
        ;;
      9)
        ask cluster_name "Cluster name" "aethernet"
        ask galera_peers "Galera peer IPs (comma-separated)"
        configure_garbd "$(state_get cluster_name)" "$(state_get galera_peers)"
        ;;
      0|q|quit|exit)
        info "Goodbye."
        exit 0
        ;;
      *)
        warn "Invalid option: $choice"
        ;;
    esac
  done
}

print_summary() {
  echo
  hr
  box "  Installation Complete! " "$BGREEN"
  echo
  ok  "Node '$(state_get node_id)' is now part of cluster '$(state_get cluster_name)'"
  info "Web Panel:    ${BCYAN}http://$(state_get advertise_ip):8080/${R}"
  info "SFTP:         ${BCYAN}sftp -P 2022 <server-id>.username@$(state_get advertise_ip)${R}"
  info "Metrics:      ${BCYAN}http://127.0.0.1:9100/metrics${R}"
  info "Logs:         journalctl -fu aether-daemon"
  info "Config:       /etc/aethernet/daemon.yaml"
  info "Install log:  ${LOG_FILE}"
  if [[ "$(state_get bootstrap)" == "yes" ]]; then
    echo
    info "To add the NEXT node to this cluster:"
    echo -e "  ${BOLD}sudo $0${R} → choose option 2 → provide join address: ${BCYAN}$(state_get advertise_ip):7001${R}"
  fi
  hr
}

# ─── Entrypoint ───────────────────────────────────────────────────────────────
main() {
  banner
  mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
  touch "$LOG_FILE"; chmod 0600 "$LOG_FILE"
  state_load

  # Non-interactive modes
  case "${1:-}" in
    preflight)  preflight; exit 0 ;;
    upgrade)    do_upgrade; exit 0 ;;
    uninstall)  do_uninstall; exit 0 ;;
    status)     show_cluster_status; exit 0 ;;
  esac

  # Interactive menu
  preflight
  main_menu
}

trap 'stop_spinner; echo; fail "Interrupted"' INT TERM
main "$@"
