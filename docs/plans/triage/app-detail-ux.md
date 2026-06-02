# App-detail — control & overview polish

**Status:** triage · **Effort:** M

## Notes → actions

1. **Quick start/stop/restart in the app header (Docker-Desktop style).**
   These actions already exist in the Stack → Services tab. The ask is convenience: lift
   start / stop / restart **all** to the app header so you don't dig into the tab. → Add a
   header control group that fans out to the existing per-service actions. **S**

2. **Per-service actions: add Stop and View logs (today only Restart).**
   Services currently expose only a Restart action. → Add Stop and View-logs alongside it
   (logs likely already stream elsewhere — wire the same source per service). **S**

3. **Shell into the app container from the UI.**
   New capability. → Open an interactive `docker exec -it {container} sh` over a WebSocket
   PTY, streamed to an xterm.js terminal in the app detail. **Guardrails:** control plane is
   deliberately sandboxed (off `vac-edge`, read-only host execs) — a shell into a *user app*
   container is a privileged action, so gate it (confirm + audit-log the session, like env
   reveal is logged). **L** — biggest item here; could be its own `upcoming/` stub.

4. **Overview info panel (right-side, first) summarizing the app.**
   New UI. Show two cards:
   - **Source** — repo (`git_url`), branch, commit, detected framework.
   - **Stack** — kind (`build_kind`, e.g. "Dockerfile (wrapped)"), service count, RAM cap,
     network (`vac-edge`).

   Most fields exist on the app/services records; framework detection may need a small addition.
   Note: for addon apps the Source card should read "from addon" instead of repo fields — see
   [addons.md](addons.md) (same DTO-exposure root cause). **M**

## Acceptance sketch

- App header has start/stop/restart-all.
- Each service row has Restart + Stop + View logs.
- A right-side overview shows Source + Stack at a glance (addon apps show "from addon").
- (Stretch) A terminal tab can shell into a running container, audit-logged.
</content>
