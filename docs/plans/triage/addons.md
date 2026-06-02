# Addons — make addon apps first-class & distinct from git apps

**Status:** triage · **Effort:** M (one root cause unlocks most of it)

## The root cause behind half these notes

Addon apps are created as template apps (`source="template"`, `template_id` set, empty
`git_url`/`git_branch`, `build_kind="compose"`) — see `store/apps.go:70` (`CreateTemplateApp`).
**But the app DTO never exposes `source`/`template_id`** (`server/handler/apps.go` `appDTO`,
~line 68). So the UI literally cannot tell an addon (Grafana) apart from a git app, which is
why addons wrongly show Database/Backups/Build/Autodeploy tabs with empty inputs.

> **Do this first:** add `source` + `template_id` (and a resolved template name/icon) to the
> app DTO. Every item below keys off it.

## Notes → actions

1. **Addons should show "Installed" if already installed.**
   Install is `addon/installer.go:69`; there's no "installed" status in the API. → Expose
   installed addons (apps where `source="template"`) and render the catalog card as Installed →
   "Open" instead of "Install". **S**

2. **Grafana-type addons shouldn't have Database/Backups tabs unless they need one.**
   They get all tabs only because the DTO can't distinguish them. → Once `source` is exposed,
   hide DB/Backups for template apps that didn't provision a managed DB. **S**

3. **Addon Settings should show "Source: from addon", and hide Autodeploy + Build.**
   Addon apps have empty `git_url`/`git_branch` and `build_kind="compose"`. → In Settings, when
   `source="template"`, replace the repo/branch/build inputs with a read-only "Installed from
   {template name}" panel; hide autodeploy. **S**

4. **Managed MariaDB should appear in the addons catalog too.**
   MariaDB is already a registered managed-DB engine (`dbprovision/mariadb.go`). → Add a catalog
   entry that provisions a managed MariaDB (reuse the addon install → provision path). **S–M**

5. **Way to uninstall addons.**
   Today addons are removed via the generic `DeleteApp`; there's no addon-aware uninstall. →
   Add an "Uninstall" action on installed-addon cards that tears down the app + its provisioned
   DB/volumes with a clear confirm ("this deletes Grafana and its data"). **M**

6. **Use [react-icons](https://react-icons.github.io/react-icons) brand icons (e.g. Grafana)
   with brand color, not a letter avatar.**
   → Add an `icon` field to the addon manifest (`addon/registry.go` Template) and render the
   brand icon in the catalog + installed cards; fall back to the letter avatar for git apps. **S**

## Acceptance sketch

- Catalog distinguishes Installed vs available; installed cards offer Open + Uninstall.
- An addon app's Settings reads "Installed from Grafana" — no repo/branch/build/autodeploy
  inputs; no Database/Backups tab unless it has a managed DB.
- MariaDB installs from the catalog; addon cards show brand icons.
</content>
