# fail2ban anti-flood jail for VAC

VAC's traffic monitor **detects** request spikes (the "request spike" dashboard
alerts) but does not block anything. This drop-in jail adds the *response*: it
tails Caddy's access log and auto-bans any single IP that floods you.

It is a **host-level** add-on — fail2ban runs on the VPS, not inside the VAC
stack. Mitigation happens at the firewall, before packets reach userspace.

## What's here

| File | Installs to |
|------|-------------|
| `filter.d/caddy-flood.conf`     | `/etc/fail2ban/filter.d/caddy-flood.conf` |
| `action.d/docker-user-drop.conf`| `/etc/fail2ban/action.d/docker-user-drop.conf` |
| `jail.d/caddy-flood.local`      | `/etc/fail2ban/jail.d/caddy-flood.local` |

### Why a custom action (the Docker gotcha)

Docker publishes Caddy's ports via DNAT. That traffic never hits the filter
`INPUT` chain, so fail2ban's **stock actions don't block Docker-published
ports** — they'd "ban" an IP and keep serving it. `docker-user-drop` inserts the
DROP into Docker's `DOCKER-USER` chain instead, which *is* on the forwarding
path. Don't swap it back to `iptables-multiport` without understanding this.

## Install

```sh
sudo apt-get install -y fail2ban        # if not already present

# from the repo root, on the VPS:
sudo cp scripts/fail2ban/filter.d/caddy-flood.conf      /etc/fail2ban/filter.d/
sudo cp scripts/fail2ban/action.d/docker-user-drop.conf /etc/fail2ban/action.d/
sudo cp scripts/fail2ban/jail.d/caddy-flood.local       /etc/fail2ban/jail.d/
```

### 1. Confirm the log path

`compose.prod.yaml` bind-mounts the Caddy access log to a stable host path,
**`/var/log/vac/caddy/access.log`** — already set as `logpath` in the jail, so
there's nothing to edit. Verify it exists:

```sh
ls -l /var/log/vac/caddy/access.log
```

If you're on an older install that still uses the `vac_caddy_logs` named volume
(no bind mount), resolve the path instead and update `logpath`:

```sh
sudo docker volume inspect -f '{{.Mountpoint}}' \
  "$(docker volume ls -q | grep caddy_logs)"   # append /access.log
```

### 2. Validate the filter against your real log BEFORE enabling

This is the step that catches a wrong regex or timestamp pattern:

```sh
sudo fail2ban-regex /var/log/vac/caddy/access.log \
  /etc/fail2ban/filter.d/caddy-flood.conf
```

Success looks like: **non-zero "Matched" lines** and **non-zero "Date template
hits"**. If date hits are 0, comment out the `datepattern` line in the filter
(fail2ban will fall back to line-read time, which is fine for a live tail).

### 3. Start it

```sh
sudo systemctl enable --now fail2ban
sudo systemctl restart fail2ban
sudo fail2ban-client status caddy-flood     # shows currently-banned IPs
```

## Tuning

- `maxretry` / `findtime` — requests-per-window that trips a ban (default
  300 / 60s, matching VAC's own monitor). Lower `maxretry` = more aggressive.
- `bantime` — how long the ban lasts (default 1h). Uncomment
  `bantime.increment` for escalating bans on repeat offenders.
- `ignoreip` — allowlist your own IPs so you can't lock yourself out.

## Manual ban / unban

```sh
sudo fail2ban-client set caddy-flood banip   78.80.225.124
sudo fail2ban-client set caddy-flood unbanip 78.80.225.124
```

## Limits

This stops **single-source / few-source** floods (like one IP doing thousands
of req/min). It does **not** stop a true distributed DDoS from thousands of
IPs — for that, put Cloudflare (or similar) in front so the flood is absorbed
upstream before it reaches the VPS.
