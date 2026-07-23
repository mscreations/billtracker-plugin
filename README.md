# Bill Tracker - an hhq plugin

A persistent, Postgres-backed [hhq](https://github.com/mscreations/happyhome-quest)
external-process plugin (see `internal/plugins` in the hhq repo for the
host-side contract this satisfies). Tracks recurring/one-off bills with a
mark-paid flow, shows upcoming unpaid bills and (optionally) synced bank/card
balances as a two-column kiosk widget, contributes bill due dates as
synthetic calendar events, and offers a settings page for managing bills and
layout, all reachable through hhq's own parent-authenticated dashboard.

It's a separate Go module (its own `go.mod`) and deployable independently of
hhq, but is built to mirror hhq's own architecture (`cmd/server` +
`internal/{config,db,models,handlers,scheduler}` + `web/templates`).

## Data storage

Bill Tracker keeps its own state in Postgres, in tables prefixed `bt_`
(`bt_bill_definitions`, `bt_bill_instances`, `bt_accounts`,
`bt_simplefin_connection`, `bt_settings`). It's common to point this at the
**same** Postgres database hhq itself uses (see `.env`), but this app never
reads hhq's own tables directly - that's not guaranteed to be true in every
deployment, and the two apps' migration histories are kept fully separate
(goose's tracking table here is `bt_goose_db_version`, not the default
`goose_db_version`, specifically to avoid colliding with hhq's own migration
tracking when sharing a database).

## Run it

```
go run ./cmd/server
```

No CLI flags - everything is environment-driven, read the same way as hhq
itself: `KEY_FILE` (a path to a file holding the value, for Kubernetes
Secret-mounted config) takes precedence over the plain `KEY` env var, which
takes precedence over a built-in default.

| Env var | Default | Purpose |
|---|---|---|
| `LISTEN_ADDR` | `:8090` | HTTP listen address |
| `DB_HOST` | *(required)* | Postgres host |
| `DB_PORT` | `5432` | Postgres port |
| `DB_NAME` | *(required)* | Postgres database name |
| `DB_USER` | *(required)* | Postgres user |
| `DB_PASSWORD` | | Postgres password |
| `DB_SSLMODE` | `disable` | Postgres SSL mode |
| `CONFIG_DIR` | `./.config` | Directory scanned for `bills.json` on startup |
| `ENCRYPTION_KEY` | *(required)* | Hex-encoded 32-byte AES-256 key. Used for SimpleFIN access URLs, and to encrypt the shared token this plugin self-issues to hhq (see "Authenticating hhq" below). Generate with `openssl rand -hex 32` |
| `BILL_INSTANCE_LOOKAHEAD_DAYS` | `60` | How far ahead recurring bill instances are generated |
| `SIMPLEFIN_REFRESH_INTERVAL_MINUTES` | `60` | How often account balances are re-fetched from SimpleFIN |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

## Defining bills

Bills can be defined two ways, and both persist to the same database:

1. **`CONFIG_DIR/bills.json`** - a JSON array, reconciled against the
   database on every startup (see `bills.json.example`). Each entry:
   `name`, `amount` (dollars), `schedule` (`"monthly"` + `day_of_month`;
   `"quarterly"` + `day_of_month` + `quarter_start_month` - `1` for
   Jan/Apr/Jul/Oct, `2` for Feb/May/Aug/Nov, `3` for Mar/Jun/Sep/Dec; or
   `"one_off"` + `one_off_date` as `YYYY-MM-DD`), optional `vendor_url`.
   Bills created this way are marked "bills.json managed" on the settings
   page and can't be edited/deleted there (edit the file and restart
   instead) - **removing an entry from the file deletes that bill (and its
   history) from the database on the next restart.** A bill with the same
   name already created through the settings UI is left untouched (not
   overwritten) if it collides with a `bills.json` entry.
2. **The settings page** - add/edit/delete bills directly; mark the current
   cycle's instance paid there too.

Either way, a bill's *schedule* (monthly-on-a-day, quarterly-on-a-day within
a chosen 3-month rotation, or one-off) is separate from its *instances* -
each due-date occurrence gets its own row so "mark paid" only affects that
cycle, not future ones. Instances are generated automatically (a background
job, plus immediately after adding/editing a bill so you don't have to
wait).

## Bank/card balances (SimpleFIN Bridge)

Optional. [SimpleFIN Bridge](https://www.simplefin.org/) connects to your
bank/card accounts and can also be a source of bill amounts for
accounts like credit cards (not yet implemented here - see Known Gaps).

To connect: get a one-time setup token from your SimpleFIN Bridge provider,
then paste it into the "SimpleFIN Bridge" section of this plugin's settings
page (reached through hhq's parent dashboard). The plugin exchanges it for a
permanent access URL, encrypts it with `ENCRYPTION_KEY`, and stores it - the
setup token itself is single-use and not retained. Balances refresh on a
timer (`SIMPLEFIN_REFRESH_INTERVAL_MINUTES`) or on demand via "Refresh now".
Individual accounts can be hidden from the kiosk balances panel without
disconnecting entirely.

## Kiosk widget layout

The kiosk widget shows two columns (bills, balances) as a single hhq plugin
widget block. Both hhq's own placement of that block (how many of hhq's 12
grid columns it spans, and its sort position among other plugin widgets)
and which bill fields show (and in what order) in the bill table are
configurable from the settings page's "Kiosk widget layout" section -
no redeploy needed.

## Register it with hhq

Copy `plugins.json.example` to `plugins.json` in whatever directory hhq's
own `CONFIG_DIR` env var points at, adjusting `base_url` to wherever this
plugin is reachable from hhq:

```json
[
  {
    "id": "bill-tracker",
    "name": "Bill Tracker",
    "base_url": "http://localhost:8090",
    "enabled": true
  }
]
```

Restart hhq (or wait for its next `PLUGIN_SYNC_INTERVAL_MINUTES` tick) and
the widget should appear on the kiosk, "Bill Tracker" should appear on the
parent dashboard's Plugins card, and unpaid bills' due dates should show up
as calendar events.

## The contract this implements

| Endpoint | Method | Purpose |
|---|---|---|
| `/register` | POST | One-time self-registration - see "Authenticating hhq" below. Unauthenticated; every other endpoint requires the token this issues. |
| `/manifest` | GET | Static metadata: display name, widget column span (1/2/3) + position (both settings-UI-configurable), whether this plugin provides calendar events |
| `/widget` | GET | HTML fragment inlined into the kiosk page (fetched server-to-server by hhq, never by the browser directly) |
| `/events` | GET | `?from=YYYY-MM-DD&to=YYYY-MM-DD` - synthetic calendar events (unpaid bill due dates) in that window, as JSON |
| `/settings` | GET, POST | A full HTML settings page, reverse-proxied through hhq's own parent-authenticated dashboard at `/parent/plugins/bill-tracker/settings` - this plugin never sees hhq's login/session, hhq only forwards requests here after its own auth check passes. Every form on this page submits to a relative URL so it round-trips correctly through the proxy regardless of the actual path the browser is on. |
| `/healthz` | GET | Liveness check, any 2xx - unauthenticated, since Kubernetes' probes send no auth header |

## Authenticating hhq

Every endpoint above except `/register` and `/healthz` requires
`Authorization: Bearer <token>` on every request (`internal/handlers/
auth_middleware.go`'s `RequireBearerToken`), rejecting anything else with
`401 Unauthorized` - otherwise this plugin's HTTP port would respond to
anyone on the network who found it, not just hhq.

There's nothing to configure for this: the token is agreed on automatically
the first time hhq successfully reaches this plugin's `POST /register`
(unauthenticated, since no token exists yet at that point) - this plugin
generates one, stores it encrypted in `bt_settings` (`ENCRYPTION_KEY`), and
returns it; hhq stores the same value encrypted in its own database and
sends it back on every subsequent request. `/register` only ever succeeds
once - a second call gets `403 Forbidden` without learning the already-
issued token. If hhq starts before this plugin is up, it retries `/register`
every 15 seconds until it succeeds (see hhq's own `internal/handlers/
plugin_bootstrap.go`), so no particular startup ordering is required.

**Recovery if hhq and this plugin ever fall out of sync** (e.g. hhq's
response from `/register` was lost in transit after this plugin had already
stored a token, so hhq never learned it): there's no automatic recovery.
Manually delete the `plugin_token` row from this plugin's `bt_settings`
table to reopen `/register`, and clear the corresponding
`hhq_plugins.encrypted_token` value in hhq's own database (`NULL` it out) so
hhq re-registers on its next restart or scheduler tick.

## Trust boundary

hhq treats a registered plugin's responses (its widget HTML, its settings
page) as trusted content, not sanitized input - they're rendered/embedded
verbatim into hhq's own pages. Only register a plugin you wrote or trust as
much as hhq itself. The `/widget` and `/settings` responses are only ever
fetched server-to-server by hhq, so their styling is self-contained inline
`<style>` rather than a linked stylesheet - in a real deployment this
plugin's `base_url` may not even be reachable from a parent's browser (e.g.
a cluster-internal Kubernetes Service DNS name), only from hhq itself.

## Known gaps

- **Deriving a bill from a linked SimpleFIN account** (e.g. auto-detecting a
  credit card's statement balance/due date) isn't implemented - the schema
  has a hook for it (`bt_bill_definitions.simplefin_account_id`) but no
  logic yet.
- **Direct vendor bill-pay** isn't implemented - a bill's `vendor_url` is
  just stored and linked, not integrated with any payment API.
- **Single replica only** - the scheduler (instance generation, SimpleFIN
  refresh) has no distributed locking.
