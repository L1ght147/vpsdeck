#!/usr/bin/env bash
# vpsdeck installer: hub, agent, update, uninstall, status.
#
#   curl -fsSL https://raw.githubusercontent.com/L1ght147/vpsdeck/main/scripts/install.sh | sudo bash
#   curl -fsSL .../install.sh | sudo bash -s -- hub [--port 8443] [--address 203.0.113.5]
#   curl -fsSL .../install.sh | sudo bash -s -- agent --url https://203.0.113.5:8443 --token TOKEN --fingerprint FP [--name NAME] [--lang ru]
#
# Environment: VPSDECK_VERSION=v0.2.0 installs a specific release instead of the latest.
set -euo pipefail

REPO="L1ght147/vpsdeck"
BIN="/usr/local/bin/vpsdeck"
CONF_DIR="/etc/vpsdeck"
DATA_DIR="/var/lib/vpsdeck"
SVC_USER="vpsdeck"
AGENT_UNIT="vpsdeck-agent"
HUB_UNIT="vpsdeck-hub"
VERSION="${VPSDECK_VERSION:-latest}"

LANG_CODE=""
CMD=""
ARG_URL=""
ARG_TOKEN=""
ARG_NAME=""
ARG_FINGERPRINT=""
ARG_PORT=""
ARG_ADDRESS=""

# ---------------------------------------------------------------- messages

declare -A EN RU
EN=(
  [title]="server monitoring system"
  [sec_hub]="Hub installation"
  [sec_agent]="Agent installation"
  [sec_update]="Update"
  [sec_uninstall]="Removal"
  [sec_status]="Status"
  [menu]="Select an action:"
  [m_hub]="Install the hub (Telegram bot; installed on a single server)"
  [agent_hint]="The agent is installed with the command issued by the bot: Main menu, Add server."
  [agent_params]="Connection parameters are missing. The agent is installed with the command issued by the bot: Main menu, Add server. Copy and run the command in full."
  [m_update]="Update"
  [m_uninstall]="Uninstall"
  [m_status]="Show status"
  [m_exit]="Exit"
  [choice]="Option"
  [bad_choice]="Invalid option."
  [need_root]="Root privileges are required. Run: curl ... | sudo bash"
  [need_systemd]="systemd is required."
  [need_tty]="No terminal is available for input. Run the installer in an interactive SSH session."
  [bad_arch]="Unsupported architecture: %s. Supported: amd64, arm64."
  [downloading]="Downloading vpsdeck %s for %s"
  [checksum_fail]="Checksum verification failed. The file may be damaged or altered. Installation aborted."
  [installed_bin]="Installed: %s, version %s"
  [agent_check]="Sending a test report to %s"
  [agent_check_fail]="Error: the hub did not accept the report. Verify the address, token and fingerprint (copy the command from the bot in full) and make sure the hub port is open in the firewall."
  [agent_ok]="The agent has been installed. Server %s will appear in the bot within a minute."
  [agent_fail]="Error: the agent failed to start. Log: journalctl -u vpsdeck-agent -n 50"
  [ask_bot_token]="Telegram bot token (issued by @BotFather)"
  [bot_check]="Verifying the bot token"
  [bot_bad]="Telegram did not accept the token. Enter it again."
  [bot_ok]="Bot: %s"
  [ask_admin]="Your Telegram ID (digits only; available from @userinfobot)"
  [bad_admin]="The ID must consist of digits only."
  [ask_port]="Port for receiving data from agents"
  [bad_port]="The port must be a number from 1 to 65535."
  [port_busy]="Port %s is in use by: %s"
  [ask_address]="The public IP address of this server could not be determined. Enter it manually (or a domain name pointing to this server)"
  [hub_address]="Hub address for agents: %s"
  [enter_default]="Enter: %s"
  [ufw_opened]="Port %s/tcp has been opened in ufw"
  [firewalld_opened]="Port %s/tcp has been opened in firewalld"
  [cloud_fw]="If your hosting provider uses its own firewall, open TCP port %s there as well."
  [hub_starting]="Starting the hub"
  [hub_fail]="Error: the hub failed to start. Log: journalctl -u vpsdeck-hub -n 50"
  [hub_ok]="The hub has been installed. Open the bot in Telegram and send /start. Servers are added from the main menu."
  [ask_local_agent]="Install the agent on this server as well?"
  [yes_no]="[Y/n]"
  [no_yes]="[y/N]"
  [nothing]="vpsdeck is not installed on this server."
  [updated]="Updated to version %s."
  [ask_remove_data]="Also delete the configuration and the hub database (%s)?"
  [removed]="vpsdeck has been removed."
  [st_unit]="%s: %s"
  [st_version]="Version: %s"
)
RU=(
  [title]="система мониторинга серверов"
  [sec_hub]="Установка хаба"
  [sec_agent]="Установка агента"
  [sec_update]="Обновление"
  [sec_uninstall]="Удаление"
  [sec_status]="Состояние"
  [menu]="Выберите действие:"
  [m_hub]="Установить хаб (Telegram-бот; устанавливается на один сервер)"
  [agent_hint]="Агент устанавливается командой, которую выдаёт бот: «Главное меню» → «Добавить сервер»."
  [agent_params]="Не указаны параметры подключения. Агент устанавливается командой, которую выдаёт бот: «Главное меню» → «Добавить сервер». Скопируйте и выполните команду полностью."
  [m_update]="Обновить"
  [m_uninstall]="Удалить"
  [m_status]="Показать состояние"
  [m_exit]="Выход"
  [choice]="Пункт"
  [bad_choice]="Неверный пункт."
  [need_root]="Требуются права root. Выполните: curl ... | sudo bash"
  [need_systemd]="Требуется systemd."
  [need_tty]="Терминал для ввода недоступен. Запустите установщик в интерактивной сессии SSH."
  [bad_arch]="Архитектура %s не поддерживается. Поддерживаются: amd64, arm64."
  [downloading]="Загрузка vpsdeck %s для %s"
  [checksum_fail]="Контрольная сумма не совпадает. Файл может быть повреждён или изменён. Установка прервана."
  [installed_bin]="Установлено: %s, версия %s"
  [agent_check]="Отправка тестового отчёта на %s"
  [agent_check_fail]="Ошибка: хаб не принял отчёт. Проверьте адрес, токен и отпечаток (команду из бота следует копировать полностью), а также что порт хаба открыт в файрволе."
  [agent_ok]="Агент установлен. Сервер %s появится в боте в течение минуты."
  [agent_fail]="Ошибка: агент не запустился. Журнал: journalctl -u vpsdeck-agent -n 50"
  [ask_bot_token]="Токен Telegram-бота (выдаётся @BotFather)"
  [bot_check]="Проверка токена бота"
  [bot_bad]="Telegram не принял токен. Введите его повторно."
  [bot_ok]="Бот: %s"
  [ask_admin]="Ваш Telegram ID (только цифры; можно узнать у @userinfobot)"
  [bad_admin]="ID должен состоять только из цифр."
  [ask_port]="Порт для приёма данных от агентов"
  [bad_port]="Порт должен быть числом от 1 до 65535."
  [port_busy]="Порт %s занят процессом: %s"
  [ask_address]="Не удалось определить публичный IP-адрес сервера. Введите его вручную (или доменное имя, указывающее на этот сервер)"
  [hub_address]="Адрес хаба для агентов: %s"
  [enter_default]="Enter: %s"
  [ufw_opened]="Порт %s/tcp открыт в ufw"
  [firewalld_opened]="Порт %s/tcp открыт в firewalld"
  [cloud_fw]="Если у хостинг-провайдера используется собственный файрвол, откройте в нём TCP-порт %s."
  [hub_starting]="Запуск хаба"
  [hub_fail]="Ошибка: хаб не запустился. Журнал: journalctl -u vpsdeck-hub -n 50"
  [hub_ok]="Хаб установлен. Откройте бота в Telegram и отправьте /start. Серверы добавляются через главное меню."
  [ask_local_agent]="Установить агент также на этот сервер?"
  [yes_no]="[Д/н]"
  [no_yes]="[д/Н]"
  [nothing]="vpsdeck на этом сервере не установлен."
  [updated]="Обновлено до версии %s."
  [ask_remove_data]="Удалить также конфигурацию и базу данных хаба (%s)?"
  [removed]="vpsdeck удалён."
  [st_unit]="%s: %s"
  [st_version]="Версия: %s"
)

# t KEY [ARGS...] prints a translated, printf-formatted message.
t() {
  local key="$1"; shift
  local fmt
  if [[ "$LANG_CODE" == "ru" ]]; then fmt="${RU[$key]:-${EN[$key]:-$key}}"; else fmt="${EN[$key]:-$key}"; fi
  # shellcheck disable=SC2059
  printf "$fmt" "$@"
}

if [[ -t 1 ]]; then
  C_RED=$'\e[31m'; C_GRN=$'\e[32m'; C_YEL=$'\e[33m'; C_BLU=$'\e[34m'; C_MAG=$'\e[35m'; C_CYN=$'\e[36m'
  C_DIM=$'\e[2m'; C_B=$'\e[1m'; C_0=$'\e[0m'
else
  C_RED=""; C_GRN=""; C_YEL=""; C_BLU=""; C_MAG=""; C_CYN=""; C_DIM=""; C_B=""; C_0=""
fi

# Color roles: cyan — values and menu numbers, magenta — section headers, blue — questions,
# green — success and defaults, yellow — warnings, red — errors, dim — progress notes.
say()  { printf '%s\n' "$*"; }
hl()   { printf '%s%s%s' "$C_CYN$C_B" "$*" "$C_0"; }
info() { printf '  %s›%s %s\n' "$C_CYN" "$C_0" "$(t "$@")"; }
ok()   { printf '%s%s%s\n' "$C_GRN$C_B" "$(t "$@")" "$C_0"; }
warn() { printf '%s%s%s\n' "$C_YEL" "$(t "$@")" "$C_0" >&2; }
die()  { printf '%s%s%s\n' "$C_RED$C_B" "$(t "$@")" "$C_0" >&2; exit 1; }
section() { printf '\n%s━━ %s ━━%s\n\n' "$C_MAG$C_B" "$(t "$@")" "$C_0"; }
item() { printf '  %s%s)%s %s\n' "$C_CYN$C_B" "$1" "$C_0" "$2"; }
question() { printf '%s?%s %s%s%s' "$C_BLU$C_B" "$C_0" "$C_B" "$*" "$C_0"; }

# ---------------------------------------------------------------- input

HAVE_TTY=0
if [[ -r /dev/tty ]] && (exec </dev/tty) 2>/dev/null; then HAVE_TTY=1; fi

# ask VAR KEY [DEFAULT] — reads from the terminal even when the script itself comes from a pipe.
ask() {
  local __var="$1" key="$2" def="${3:-}" prompt reply
  [[ $HAVE_TTY == 1 ]] || die need_tty
  prompt="$(question "$(t "$key")")"
  [[ -n "$def" ]] && prompt+=" ${C_DIM}($(t enter_default "${C_0}${C_GRN}${C_B}$def${C_0}${C_DIM}"))${C_0}"
  read -r -p "$prompt: " reply </dev/tty || true
  printf -v "$__var" '%s' "${reply:-$def}"
}

ask_secret() {
  local __var="$1" key="$2" reply
  [[ $HAVE_TTY == 1 ]] || die need_tty
  read -r -s -p "$(question "$(t "$key")"): " reply </dev/tty || true
  echo >&2
  printf -v "$__var" '%s' "$reply"
}

# confirm KEY yes|no [ARGS...] — returns 0 for yes.
confirm() {
  local key="$1" def="${2:-yes}" reply hint
  shift 2
  [[ $HAVE_TTY == 1 ]] || { [[ "$def" == yes ]]; return; }
  if [[ "$def" == yes ]]; then hint="$(t yes_no)"; else hint="$(t no_yes)"; fi
  read -r -p "$(question "$(t "$key" "$@")") ${C_DIM}$hint${C_0} " reply </dev/tty || true
  if [[ -z "$reply" ]]; then [[ "$def" == yes ]]; return; fi
  [[ "$reply" == [yYдД]* ]]
}

choose_lang() {
  [[ -n "$LANG_CODE" ]] && return
  if [[ $HAVE_TTY != 1 ]]; then LANG_CODE="en"; return; fi
  local reply
  say ""
  item 1 "English"
  item 2 "Русский"
  say ""
  read -r -p "$(question "Language / Язык") ${C_DIM}(Enter — ${C_0}${C_GRN}${C_B}1${C_0}${C_DIM})${C_0}: " reply </dev/tty || true
  if [[ "$reply" == 2 ]]; then LANG_CODE="ru"; else LANG_CODE="en"; fi
}

# ---------------------------------------------------------------- checks

require_root() { [[ $EUID -eq 0 ]] || die need_root; }

require_systemd() {
  if ! command -v systemctl >/dev/null || [[ ! -d /run/systemd/system ]]; then die need_systemd; fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die bad_arch "$(uname -m)" ;;
  esac
}

ensure_tools() {
  local missing=()
  for c in curl sha256sum ss; do command -v "$c" >/dev/null || missing+=("$c"); done
  if ((${#missing[@]})); then
    if command -v apt-get >/dev/null; then
      apt-get update -qq && apt-get install -y -qq curl coreutils iproute2 >/dev/null
    elif command -v dnf >/dev/null; then
      dnf install -y -q curl coreutils iproute >/dev/null
    fi
  fi
}

ensure_user() {
  id -u "$SVC_USER" >/dev/null 2>&1 || useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$SVC_USER"
}

# ---------------------------------------------------------------- binary

release_url() {
  if [[ "$VERSION" == latest ]]; then
    printf 'https://github.com/%s/releases/latest/download/%s' "$REPO" "$1"
  else
    printf 'https://github.com/%s/releases/download/%s/%s' "$REPO" "$VERSION" "$1"
  fi
}

BIN_INSTALLED=0

install_binary() {
  [[ $BIN_INSTALLED == 1 ]] && return
  detect_arch
  local asset="vpsdeck_linux_${ARCH}" tmp
  tmp="$(mktemp -d)"
  info downloading "$(hl "$VERSION")" "linux/$ARCH"
  curl -fsSL --retry 3 -o "$tmp/$asset" "$(release_url "$asset")"
  curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$(release_url checksums.txt)"
  if ! (cd "$tmp" && grep " ${asset}\$" checksums.txt | sha256sum -c --status); then
    rm -rf "$tmp"
    die checksum_fail
  fi
  install -m 0755 "$tmp/$asset" "$BIN"
  rm -rf "$tmp"
  BIN_INSTALLED=1
  info installed_bin "$BIN" "$(hl "$("$BIN" version)")"
}

# ---------------------------------------------------------------- systemd

write_unit() {
  local unit="$1" desc="$2" args="$3" extra="${4:-}"
  cat >"/etc/systemd/system/$unit.service" <<EOF
[Unit]
Description=$desc
After=network-online.target
Wants=network-online.target

[Service]
User=$SVC_USER
Group=$SVC_USER
ExecStart=$BIN $args
Restart=always
RestartSec=10
${extra}NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes

[Install]
WantedBy=multi-user.target
EOF
}

start_unit() {
  systemctl daemon-reload
  systemctl enable "$1" >/dev/null 2>&1
  systemctl restart "$1"
  sleep 3
  systemctl is-active --quiet "$1"
}

# ---------------------------------------------------------------- agent

install_agent() {
  local url="$ARG_URL" token="$ARG_TOKEN" fp="$ARG_FINGERPRINT" name="${ARG_NAME:-$(hostname -s)}"
  # The agent is installed only with the command the bot issues, which carries all three.
  [[ -n "$url" && -n "$token" && -n "$fp" ]] || die agent_params
  url="${url%/}"
  [[ "$url" == http://* || "$url" == https://* ]] || url="https://$url"
  name="${name//[^A-Za-z0-9._-]/-}"
  token="${token//[^A-Za-z0-9_-]/}"
  fp="${fp//[^A-Fa-f0-9]/}"

  install_binary
  ensure_user
  install -d -m 0750 -o root -g "$SVC_USER" "$CONF_DIR"
  umask 027
  cat >"$CONF_DIR/agent.yaml" <<EOF
hub_url: "$url"
token: "$token"
fingerprint: "$fp"
name: "$name"
interval: 60s
update_interval: 1h
EOF
  chown root:"$SVC_USER" "$CONF_DIR/agent.yaml"
  chmod 0640 "$CONF_DIR/agent.yaml"

  # A real report proves the address, the certificate pin and the token in one go.
  info agent_check "$(hl "$url")"
  if ! runuser -u "$SVC_USER" -- "$BIN" agent -config "$CONF_DIR/agent.yaml" -once; then
    die agent_check_fail
  fi

  write_unit "$AGENT_UNIT" "vpsdeck agent" "agent -config $CONF_DIR/agent.yaml"
  if start_unit "$AGENT_UNIT"; then ok agent_ok "$(hl "$name")$C_GRN$C_B"; else die agent_fail; fi
}

# ---------------------------------------------------------------- hub

port_user() { ss -ltnpH "sport = :$1" 2>/dev/null | sed -n 's/.*users:(("\([^"]*\)".*/\1/p' | head -n1; }
port_busy() { [[ -n "$(ss -ltnH "sport = :$1" 2>/dev/null)" ]]; }

first_free_port() {
  local p
  for p in 8443 9443 10443 18443 28443; do port_busy "$p" || { echo "$p"; return; }; done
  echo 38443
}

# public_ip prints this server's public IPv4: a public address on an interface,
# or the one seen from the internet (for servers behind NAT).
public_ip() {
  local ip
  for ip in $(hostname -I 2>/dev/null || true); do
    [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue
    [[ "$ip" =~ ^(10\.|127\.|169\.254\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.) ]] && continue
    echo "$ip"; return
  done
  curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
    || curl -4 -fsS --max-time 5 https://ipv4.icanhazip.com 2>/dev/null \
    || true
}

open_firewall() {
  local port="$1"
  if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q '^Status: active'; then
    ufw allow "$port/tcp" >/dev/null && info ufw_opened "$port"
  elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
    firewall-cmd -q --permanent --add-port="$port/tcp" && firewall-cmd -q --reload && info firewalld_opened "$port"
  fi
  info cloud_fw "$port"
}

install_hub() {
  local bot_token admin bot_name port address public_url

  while :; do
    ask_secret bot_token ask_bot_token
    info bot_check
    bot_name="$(curl -fsS --max-time 10 "https://api.telegram.org/bot${bot_token}/getMe" 2>/dev/null \
      | sed -n 's/.*"username":"\([^"]*\)".*/\1/p')" || true
    [[ -n "$bot_name" ]] && { info bot_ok "$(hl "@$bot_name")"; break; }
    warn bot_bad
  done

  while :; do
    ask admin ask_admin
    [[ "$admin" =~ ^[0-9]+$ ]] && break
    warn bad_admin
  done

  port="$ARG_PORT"
  while :; do
    [[ -n "$port" ]] || ask port ask_port "$(first_free_port)"
    if [[ ! "$port" =~ ^[0-9]+$ ]] || ((port < 1 || port > 65535)); then warn bad_port; port=""; continue; fi
    if port_busy "$port"; then warn port_busy "$port" "$(port_user "$port")"; port=""; continue; fi
    break
  done

  address="$ARG_ADDRESS"
  [[ -n "$address" ]] || address="$(public_ip | tr -d '[:space:]')"
  while [[ -z "$address" ]]; do ask address ask_address; done
  address="${address#http://}"; address="${address#https://}"; address="${address%%/*}"; address="${address%%:*}"
  public_url="https://$address:$port"
  info hub_address "$(hl "$public_url")"

  install_binary
  ensure_user
  install -d -m 0750 -o root -g "$SVC_USER" "$CONF_DIR"
  install -d -m 0750 -o "$SVC_USER" -g "$SVC_USER" "$DATA_DIR"
  umask 027
  cat >"$CONF_DIR/hub.yaml" <<EOF
# vpsdeck hub — restart after editing: systemctl restart vpsdeck-hub
listen: ":$port"
public_url: "$public_url"
bot_token: "$bot_token"
admins: [$admin]
lang: $LANG_CODE
db_path: $DATA_DIR/hub.db
digest_time: "09:00"
timezone: "$(timedatectl show -p Timezone --value 2>/dev/null || echo UTC)"
thresholds:
  disk_warn: 85
  disk_crit: 95
  cpu: 90
  mem: 90
  sustain: 10m
  offline_after: 3m
EOF
  chown root:"$SVC_USER" "$CONF_DIR/hub.yaml"
  chmod 0640 "$CONF_DIR/hub.yaml"

  open_firewall "$port"

  # Binding ports below 1024 needs this capability, since the hub does not run as root.
  local caps=""
  ((port < 1024)) && caps=$'AmbientCapabilities=CAP_NET_BIND_SERVICE\n'
  write_unit "$HUB_UNIT" "vpsdeck hub" "hub -config $CONF_DIR/hub.yaml" \
    "StateDirectory=vpsdeck"$'\n'"StateDirectoryMode=0750"$'\n'"$caps"
  info hub_starting
  start_unit "$HUB_UNIT" || die hub_fail

  if confirm ask_local_agent yes; then
    local tok fp
    tok="$(runuser -u "$SVC_USER" -- "$BIN" hub add-server -config "$CONF_DIR/hub.yaml" -name "$(hostname -s)" -print token)"
    fp="$(runuser -u "$SVC_USER" -- "$BIN" hub fingerprint -config "$CONF_DIR/hub.yaml")"
    ARG_URL="https://127.0.0.1:$port" ARG_TOKEN="$tok" ARG_FINGERPRINT="$fp" ARG_NAME="$(hostname -s)"
    install_agent
  fi

  say ""
  ok hub_ok
}

# ---------------------------------------------------------------- update / uninstall / status

unit_exists() { [[ -f "/etc/systemd/system/$1.service" ]]; }

do_update() {
  [[ -x "$BIN" ]] || die nothing
  install_binary
  local u
  for u in "$HUB_UNIT" "$AGENT_UNIT"; do
    unit_exists "$u" && systemctl restart "$u"
  done
  ok updated "$("$BIN" version)"
}

do_uninstall() {
  [[ -x "$BIN" ]] || unit_exists "$AGENT_UNIT" || unit_exists "$HUB_UNIT" || die nothing
  local u
  for u in "$AGENT_UNIT" "$HUB_UNIT"; do
    if unit_exists "$u"; then
      systemctl disable --now "$u" >/dev/null 2>&1 || true
      rm -f "/etc/systemd/system/$u.service"
    fi
  done
  systemctl daemon-reload
  rm -f "$BIN"
  if confirm ask_remove_data no "$CONF_DIR, $DATA_DIR"; then
    rm -rf "$CONF_DIR" "$DATA_DIR"
  fi
  ok removed
}

do_status() {
  if [[ -x "$BIN" ]]; then info st_version "$("$BIN" version)"; else warn nothing; return; fi
  local u
  for u in "$HUB_UNIT" "$AGENT_UNIT"; do
    unit_exists "$u" && say "$(t st_unit "$u" "$(systemctl is-active "$u" 2>/dev/null || true)")"
  done
  return 0
}

# ---------------------------------------------------------------- main

parse_args() {
  while (($#)); do
    case "$1" in
      hub|agent|update|uninstall|status) CMD="$1" ;;
      --url) ARG_URL="${2:-}"; shift ;;
      --token) ARG_TOKEN="${2:-}"; shift ;;
      --fingerprint) ARG_FINGERPRINT="${2:-}"; shift ;;
      --port) ARG_PORT="${2:-}"; shift ;;
      --address) ARG_ADDRESS="${2:-}"; shift ;;
      --name) ARG_NAME="${2:-}"; shift ;;
      --lang) LANG_CODE="${2:-}"; shift ;;
      --version) VERSION="${2:-latest}"; shift ;;
      *) ;;
    esac
    shift
  done
  [[ "$LANG_CODE" == ru || -z "$LANG_CODE" ]] || LANG_CODE="en"
}

menu() {
  say ""
  say "${C_CYN}${C_B}◆ vpsdeck${C_0}  ${C_DIM}$(t title)${C_0}"
  say ""
  say "${C_B}$(t menu)${C_0}"
  item 1 "$(t m_hub)"
  item 2 "$(t m_update)"
  item 3 "$(t m_uninstall)"
  item 4 "$(t m_status)"
  item 0 "${C_DIM}$(t m_exit)${C_0}"
  say ""
  say "${C_DIM}$(t agent_hint)${C_0}"
  say ""
  local c; ask c choice
  case "$c" in
    1) CMD=hub ;; 2) CMD=update ;; 3) CMD=uninstall ;; 4) CMD=status ;;
    0) exit 0 ;;
    *) die bad_choice ;;
  esac
}

main() {
  parse_args "$@"
  choose_lang
  require_root
  require_systemd
  ensure_tools
  [[ -n "$CMD" ]] || menu
  section "sec_$CMD"
  case "$CMD" in
    hub) install_hub ;;
    agent) install_agent ;;
    update) do_update ;;
    uninstall) do_uninstall ;;
    status) do_status ;;
  esac
}

main "$@"
