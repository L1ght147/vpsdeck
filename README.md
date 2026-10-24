# vpsdeck

**English** · [Русский](README.ru.md)

Monitor your VPS fleet from a Telegram bot.

- **Alerts** when a server goes offline, a disk fills up, or CPU / RAM stay high — and when it recovers.
- **Server cards** in the chat: disks, CPU, RAM, load, uptime, pending updates, reboot required.
- **Installs on almost any server**: one static binary, no web server, no domain, no Docker. It does not touch nginx, Caddy or anything already running.
- English and Russian.

```
[agent] [agent] [agent] ──HTTPS, every 60 s──▶ [hub] ──▶ Telegram bot
```

The **agent** runs on every server, collects metrics and pushes them to the hub. It opens no ports and runs as an unprivileged user.
The **hub** runs on one server: it stores the latest reports (SQLite), raises alerts and runs the bot. It listens on one TCP port of your choice (default `8443`) with its own self-signed certificate; agents pin that certificate by fingerprint, so the connection is encrypted and cannot be spoofed — no domain needed.

## What it watches

| Metric | Alert |
| --- | --- |
| Online / offline | no report for 3 min → offline, and "back online" |
| Disk usage per mount (+ inodes) | ≥ 85 % warning, ≥ 95 % critical |
| CPU, RAM, swap, load average | CPU or RAM ≥ 90 % for 10 min |
| Pending apt updates, security updates | daily digest |
| Reboot required | daily digest |

Alerts fire only when the state changes and can be muted per server for 1 h or 24 h.

## Requirements

- Linux with systemd, amd64 or arm64. Update checks use apt (Ubuntu, Debian); elsewhere metrics work without them.
- For the hub: a bot from [@BotFather](https://t.me/BotFather) and one free TCP port reachable by your other servers.

## Install

### 1. Hub (once, on any one server)

```bash
curl -fsSL https://raw.githubusercontent.com/L1ght147/vpsdeck/main/scripts/install.sh | sudo bash
```

Choose the language, then **Install hub**. The installer asks for:

- the bot token from @BotFather;
- your Telegram user ID ([@userinfobot](https://t.me/userinfobot) shows it);
- the port for agents (suggests a free one, `8443` by default);
- the address agents will use — your server's public IP is detected for you.

It opens the port in ufw / firewalld if they are active (open it in your hosting provider's firewall yourself), starts the hub, and can also put an agent on the same server. The bot then sends you "hub is running".

### 2. Agents (on every server)

Open the bot, press **Add server** and send a name, e.g. `web-1`. The bot replies with a one-line command; run it on the server:

```bash
curl -fsSL https://raw.githubusercontent.com/L1ght147/vpsdeck/main/scripts/install.sh | sudo bash -s -- agent --url https://203.0.113.5:8443 --token <TOKEN> --name web-1 --lang en --fingerprint <FINGERPRINT>
```

The installer sends a test report first, so a wrong address, token or closed port shows up immediately. The server appears in the bot within a minute.

### Update, uninstall, status

Once a day the hub checks GitHub for a new release and, if one is available, the bot sends a notification. Updates are never installed by the bot.

Run the installer again and pick **Update**, **Uninstall** or **Status**, or pass it directly:

```bash
curl -fsSL https://raw.githubusercontent.com/L1ght147/vpsdeck/main/scripts/install.sh | sudo bash -s -- update
```

## Bot

The bot has a single command, `/start`, which opens the main menu. Everything else is inline buttons:

- **Servers** — list with status and disk usage; open a server for its card (CPU, RAM, disks, updates) with Refresh, Packages, Mute and Remove.
- **Require attention** — servers that are offline, awaiting connection or over a threshold, with the reasons.
- **Updates** — servers with pending updates and their package lists.
- **Add server** — send a name (or use the default) and get the install command.
- **Settings** — language and the current alert thresholds.

Only the Telegram IDs listed in `admins` can use the bot.

## Configuration

`/etc/vpsdeck/hub.yaml` (restart with `systemctl restart vpsdeck-hub`):

```yaml
listen: ":8443"
public_url: "https://203.0.113.5:8443"   # address agents connect to
bot_token: "123456:ABC..."
admins: [123456789]                      # several IDs are allowed
lang: en                                 # en | ru
db_path: /var/lib/vpsdeck/hub.db
digest_time: "09:00"                     # daily updates digest
timezone: "Europe/Berlin"
thresholds:
  disk_warn: 85
  disk_crit: 95
  cpu: 90
  mem: 90
  sustain: 10m                           # how long CPU/RAM must stay high
  offline_after: 3m
# tls_disable: true                      # only if the hub sits behind your own HTTPS proxy
# disable_update_check: true             # do not check GitHub for new releases
```

`/etc/vpsdeck/agent.yaml`:

```yaml
hub_url: "https://203.0.113.5:8443"
token: "..."
fingerprint: "..."     # SHA-256 of the hub certificate; `vpsdeck hub fingerprint` prints it
name: "web-1"
interval: 60s
update_interval: 1h
```

The agent does not run `apt update` itself; Ubuntu and Debian refresh package lists on their own (`apt-daily.timer`).

## Security

- Agents only make outgoing HTTPS requests; nothing listens on them.
- The hub's certificate is generated on first start and pinned by every agent.
- Each server has its own token; the hub stores only its SHA-256 hash. Removing a server revokes it.
- The installer verifies downloads against `checksums.txt`. Services run as the unprivileged `vpsdeck` user with systemd sandboxing.

## Development

```bash
go test ./...
go run ./cmd/vpsdeck agent -print     # print a report from this machine
go run ./cmd/vpsdeck hub -dev         # hub on 127.0.0.1:8080 over plain HTTP, without the bot
```

Releases: push a tag `vX.Y.Z`; GitHub Actions builds linux/amd64 and linux/arm64 with GoReleaser.

## License

MIT
