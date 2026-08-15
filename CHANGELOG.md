# Changelog

All notable changes to OpenSourceBackup are documented in this file.
Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/).

🇩🇪 [Deutsche Version](#deutsch) weiter unten.

---

## [English]

---

## [Unreleased] — 2026-08-15

### Security

- **E2.1b-1 authorized repository-credential resolution boundary.** Future
  secret resolution now has a tested server-side derivation from authenticated
  system and job to policy, repository, derived purpose and active credential
  reference. Callers cannot select a provider, reference, repository or
  purpose. No secret is resolved or delivered in this slice; Vault/KMS, API and
  agent delivery remain deliberately deferred.
- **Fail-closed credential states.** Disabled, revoked, missing and ambiguous
  references are denied. The future legacy `RESTIC_PASSWORD` fallback is
  explicitly forbidden whenever a managed reference exists for that purpose.

## [Unreleased] — 2026-06-08

### Security — API hardening & CSP

- **Authorization hardened across the API (RBAC).** GDPR export/purge and the
  audit log are now **admin-only**; enrollment-token creation and
  systems/jobs/snapshots writes are role-gated (operator to create/update,
  admin to delete) — matching the existing repository/policy pattern.
- **SSRF protection on webhook URLs.** Notification webhook targets are validated
  before storage and dispatch; private/loopback/link-local and cloud-metadata
  ranges (e.g. 169.254.169.254) and non-http(s) schemes are rejected.
- **Rate limiting on enrollment endpoints** (`POST /v1/agent/enroll`,
  `POST /v1/systems/{id}/enrollment-token`) — 2/min per IP.
- **CSRF token comparison is constant-time** (`crypto/subtle`).
- **Content-Security-Policy hardened (R-01).** `script-src 'self'` — the build
  emits no inline scripts, so `'unsafe-inline'` was dropped (primary XSS vector
  closed). Dead font-CDN origins removed (no web fonts are loaded). `style-src`
  keeps `'unsafe-inline'` by design (React inline styles) — now documented.
- **No raw store errors leaked to clients** — sentinel errors map to safe
  messages, everything else collapses to “internal error” (details logged
  server-side).

### Added

- **Stale-Job-Reaper.** Running backup/retention jobs that go silent are
  auto-failed every 10 min — two-tier grace (silent-after-progress 30 min, or
  no-progress-since-start 12 h) so a crashed agent or network loss never leaves
  a job stuck “running”. `internal/scheduler` + `catalog.JobStore.FailStaleJobs`.
- **Email / SMTP notifications.** Real SMTP sender (STARTTLS on 587, implicit TLS
  on 465); credentials come from the environment only
  (`SMTP_HOST/FROM/PORT/USERNAME/PASSWORD/TLS`) — never the DB, never argv, never
  logged. Recipient validation + CRLF header-injection protection. Settings UI
  exposes the email channel.
- **Restore-target path guardrails (B_RESTORE_REPO_SELECT).** The New Restore Test
  modal assesses the target path (empty/safe/caution/danger) and blocks
  filesystem roots and protected system directories. Advisory — the agent's
  `validateRestorePath` remains the authoritative boundary. Repository selection
  was already wired end to end.
- **Cockpit redesign.** Operations-centre layout — KPI cards (systems, running
  jobs, errors, restore verification), large per-system live-status cards with
  progress/throughput/Stop, a run-now panel, a repository sidebar and a slim
  recent-jobs table — replacing the table-only view. Small components under
  `web/src/components/cockpit/`.
- **Frontend test runner (Vitest).** The web project had none; adds unit tests
  for `lib/pathSafety` and the cockpit helpers and a `test` script.
- **Pre-push quality gate.** Versioned git hook (`scripts/hooks/pre-push`):
  secret/private-data scan + `go vet`/`go test` + `tsc` + `vitest` on every push.

### Fixed

- **Scheduler never fired (5-field cron).** `cron.New(cron.WithSeconds())`
  expected 6-field expressions while all policies use 5-field, so
  `scheduled_policies` was 0 and no scheduled backups ran. Now `cron.New()`.
- **Backup failures were undiagnosable.** `runBackup` discarded restic's stderr,
  so failures surfaced only as “exit status N”. stderr is now captured (bounded,
  8 KiB) and included in the error.
- **Restore path “/” slipped through.** The path-safety check normalised a lone
  “/” to empty and fell through to a soft warning; it is now treated as a
  filesystem root (blocked). Caught by a unit test.

## [Unreleased] — 2026-06-06

### Fixed — Agent reliability (backups had silently stopped)

- **Restic binary resolution (`exec.ErrDot`)** — backups failed instantly with
  `restic init: exec: "restic": cannot run executable found relative to current directory`.
  When `RESTIC_BIN` was unset the agent ran restic by bare name; Go's `os/exec`
  refuses a binary resolved relative to the current directory. The agent now
  resolves restic to an **absolute path** (prefers the binary next to the agent,
  else a PATH lookup made absolute). `internal/agent/restic/runner.go`
- **Agent autostart only on login** — the Windows agent was started via
  `HKCU\Run`, so it ran **only while a user was logged in**; backups stopped
  unnoticed whenever nobody was signed in. It now installs as a real **Windows
  service** (Automatic start, runs without login, SCM restart-on-crash). For an
  SMB/UNC repo the service runs **under a user account** (LocalSystem has no
  network credential); the installer grants that account the *Log on as a
  service* right via LSA. Local/cloud repos use LocalSystem.
- **Windows service never started (SCM)** — service start (no args) fell into the
  interactive path instead of `svc.Run()`. Now branches on `service.Interactive()`
  so the service registers with the SCM. `cmd/agent/main.go`
- **Backups dead after a crash (stale lock)** — a crash left the previous run's
  restic lock behind, blocking every future backup with `repository is already
  locked` (exit status 1) until cleared by hand. The agent now removes stale
  locks before each backup (`restic unlock`, stale-only — a live lock from
  another host on the same repo is preserved). `internal/agent/restic/runner.go`
- **Agent unobservable as a service** — service stdout is discarded; the agent
  now writes to `AGENT_LOG_FILE` so job logs survive. `cmd/agent/main.go`
- **`install-agent.ps1` crashed on Windows PowerShell 5.1** — used the PS7-only
  `??` operator; replaced so the `irm … | iex` one-liner works on default Windows.

### Added

- **B_JOB_CANCEL** — operator stop for a running backup (operational safety): protect
  an important machine without a brutal `restic` kill. `POST /v1/jobs/{id}/cancel`
  flags the job (audited: who/when/why); the agent's watcher cancels restic via
  context and reports the job as **`cancelled`** (not `failed`). Operator role;
  the stale lock self-heals via auto-unlock. Migration 000022.
- **B_JOB_PROGRESS** — live backup progress: the agent streams restic's progress
  (percent, bytes, files, throughput) to the control plane via
  `PUT /v1/agent/jobs/{id}/progress`; `GET /v1/jobs` exposes it. Throttled to ~2s,
  throughput computed agent-side. **Privacy:** only aggregate counters are stored —
  restic's `current_files` (paths) is never forwarded. A completed job is pinned
  to 100%. Migration 000021. Foundation for the live cockpit view.
- **B_LOWPRIO_RESTIC** — agent-spawned restic runs at lowered CPU priority on
  Windows (`BELOW_NORMAL_PRIORITY_CLASS`) so a long backup yields to interactive
  work; no-op on Linux/FreeBSD. Opt out with `AGENT_LOW_PRIORITY_RESTIC=false`
  (default on). Reduces CPU contention only — no crash-prevention claim.
- **Restic passwordless repos** — pass `--insecure-no-password` on verify and the
  generic restic command when no password is set; `init --quiet` for clean logs.

### Security / Chore

- **`.gitignore` hardened** — excludes all private/sensitive data: `certs/` fully
  (+ global `*.key/*.pem/*.crt/*.p12/*.pfx`), `.env` (keeps `.env.example`),
  `data/` (restore sandboxes), `tooldesign/`, and `osb-server-*` build artifacts.

---

## [Unreleased] — 2026-06-01 (continued)

### Added — Productivity & Operations

- **B_LOGIN** — Login page with email/password, session cookie, logout button in topbar
- **Dark/Light Mode** — 🌙 toggle in topbar, persisted in localStorage
- **B_NOTIFY** — Webhook notification channels in Settings (Slack/Teams/Discord/custom)
  - Minimum severity filter (info / warning / critical)
  - Test-webhook button
  - Morning Report via webhook (`internal/notify/report.go`)
- **Bandwidth-Throttling** — Policy → General → bandwidth limit KB/s (passed as `--limit-upload` to restic)
- **Backup Pause-Window** — Scheduler now enforces `window_start`/`window_end` before dispatching; supports overnight windows (e.g. 22:00–06:00)
- **Backup Verify Foundation** — `restic.Verify()` with optional `--read-data`; new job type `verify`
- **Agent Auto-Update Foundation** — Heartbeat response includes `recommended_version` + `update_available`; no binary download yet (safe)
- **B_STABILIZE** — Tests: backup window (incl. midnight-crossing), bandwidth flag, notification severity, morning report no-secrets check

### Fixed

- Exit status 3 from restic on Windows treated as partial success (locked files)
- Activity chart x-axis labels rendered as HTML (crisp, not tiny SVG text)
- Activity stats bar brighter with border separator
- BrowserRouter `basename="/ui"` — fixes "No routes matched /ui/" error in LXC

### Known Status (honest)

| Feature | Status |
|---|---|
| Backup Verify | Foundation only — scheduler + agent pipeline not yet wired end-to-end |
| Agent Auto-Update | Foundation only — no binary download |
| B_NOTIFY webhooks | UI + send logic built; API endpoint for channel CRUD pending |
| Morning Report | Logic built; scheduled dispatch pending |

---

## [Unreleased] — 2026-06-01

### Added — Dashboard & UI

- **B_DASH** — Dashboard v2: KPI row, Recovery Score card with explained deductions,
  Restore Verification Donut (SVG, no external lib), Quick Actions in sidebar
- **Activity Chart** — 24h bar chart (Backups / Restore Tests / Failures), SVG, no library
- **Alerts Preview Panel** — top active alerts embedded in dashboard with severity + action
- **Recent Evidence Panel** — last 6 audit events embedded in dashboard
- **Repository Health Table** — Immutability badge, Encryption, Verified count, Last Backup/Restore
- **Agent Activity** — Online/Idle/Offline donut + Last Seen list with status dots

### Added — Security & DSGVO

- **B_IMM** — Immutable Repository Checks: `immutable_mode` field (none/object_lock/worm/append_only/unknown),
  Migration 000015, Repository Health API (`GET /v1/repositories/health`, `GET /v1/repositories/{id}/health`)
- **B_AUD** — Structured Audit Log: `actor_type` + `severity` fields (Migration 000016),
  Fluent Builder (`audit.Event(...).By(...).Severity(...).Build()`),
  Actions wired in repositories, policies, enrollment, retention handlers,
  Evidence page (`/evidence`) with filter by severity/category
- **B_RBAC** — Multi-user RBAC: `users` table (Migration 000017), Admin/Operator/Viewer roles,
  Bootstrap admin on first start (ADMIN_EMAIL + ADMIN_PASSWORD),
  `POST /auth/login`, `GET /auth/me`, user management API (`/v1/users`),
  Protect destructive endpoints by role, session invalidation on role change,
  Last-admin protection (cannot delete or demote last admin)
- **B_TLS** — TLS hardening: `TLS_REQUIRED=true` enforcement flag (refuses HTTP startup),
  HTTP→HTTPS redirect server (`HTTP_REDIRECT_ADDR`),
  `gencert` accepts LAN IP args (`make certs TLS_IP=192.168.1.100`),
  `Makefile`: `make certs TLS_IP=...`

### Added — Observability

- **B15** — Prometheus metrics (`GET /metrics`): jobs, restore tests, agents online/idle/offline,
  snapshots verified ratio, recovery score — all same truth as dashboard
- **B_HB** — Agent Heartbeat: `PUT /v1/agent/heartbeat`, `systems.last_seen` updated on every poll,
  Agent Activity Donut (Online ≤2min / Idle ≤15min / Offline), Last Seen list
- **B_SC v2** — Backup Health Score v2: `internal/health/` canonical package,
  `GET /v1/health/score`, `GET /v1/health/activity` (hourly buckets),
  12 deduction codes (restore_test_stale, backup_stale_24h, agents_offline, repos_not_immutable …),
  Positive factors shown alongside deductions, Score v2.0 label
- **B_ALERTS** — Alerts page (`/alerts`): stateless alerts from health score deductions,
  `GET /v1/health/alerts`, severity filter, category filter, action guidance per alert,
  "Go to X" navigation buttons, All-clear state

### Added — Retention

- **B_RET** — Retention + Prune Engine: typed retention fields on policies
  (keep_last/daily/weekly/monthly/yearly, Migration 000014),
  `backup_jobs.type` column (backup | retention),
  Agent retention routes: list jobs, validate candidates (safety rule), complete, fail,
  **Hard safety rule**: last restore-tested snapshot never deleted
  (enforced server-side, agent cannot bypass)

### Added — Installer & DevOps

- **Proxmox LXC auto-installer** (`scripts/install-proxmox.sh`): auto-detects free container ID,
  downloads Debian 12 template, creates LXC, installs inside
- **Windows local all-in-one installer** (`scripts/install-local.ps1`, NSIS `local-installer.nsi`)
- **MSI installer** (`build/windows/agent.wxs`) for enterprise SCCM/GPO deployment
- **`scripts/build-release.ps1`**: builds all platforms + MSI + EXE + SHA256 checksums
- **`.env.example`**: all environment variables documented
- **GitHub Action** (`.github/workflows/sync-main.yml`): auto-syncs master→main after every push

### Fixed

- **RBAC redirect loop**: `/ui/` was not in public paths → infinite redirect, fixed
- **RBAC dev mode**: auth only enforced when BOTH `ADMIN_EMAIL` AND `ADMIN_PASSWORD` set
- **CSRF token not sent**: added `X-CSRF-Token` header to all POST/PUT/DELETE in frontend
- **Vite base path**: set `base: '/ui/'` so assets resolve correctly when served under `/ui/`
- **API URL fallback**: `BASE` defaults to `''` (relative) not `http://localhost:8080`
- **TypeScript errors**: unused `VERSION`, `scoreResult`, `useEffect`, `totalH`, `size` vars
- **Old calcRecoveryScore**: removed from Dashboard.tsx (now computed by Go backend)
- **Audit log query**: added `?limit=N` parameter support

---

## [0.1.0] — 2026-05-31

### Added — Core Platform

- **Control Plane** (Go 1.22+): REST API for managing systems, repositories, policies, jobs, snapshots, and restore tests
- **PostgreSQL 16** catalog with 13 database migrations
- **Redis 7** for future caching/pub-sub
- **Scheduler**: cron-based policy execution, Dead-Man's Switch for missed backups
- **Snapshot catalog**: tracks every successful backup with size, duration, and file count

### Added — Backup Agent

- **Cross-platform agent binary** (single codebase, four targets):
  - Windows x64 — runs as a Windows Service
  - Linux x64 / ARM64 — runs as a systemd unit
  - FreeBSD x64 — runs as an rc.d service (OPNsense / pfSense)
- **Enrollment protocol**: one-time token → permanent agent token (SHA-256 hashed in DB)
- **Restic runner**: backup, restore, restore-test with sandbox isolation
- **Windows UtimesNano fix**: treats timestamp-only exit-code-1 as success

### Added — Web Dashboard

- **React 18 + TypeScript + Vite** SPA served directly from the Go binary
- Pages: Dashboard (health overview), Systems, Agents, Repositories, Policies, Jobs, Restore Tests
- **Job Detail Panel** with live auto-refresh every 3s during active jobs
- **Enrollment Wizard**: 4-step guide with platform-specific install commands (Windows/Linux/FreeBSD)
- 9 repository types with descriptions and setup hints
- Engine selector: Restic, Borg, pgBackRest, Velero

### Added — Installer Suite

- `scripts/install-proxmox.sh` — auto-creates Debian 12 LXC container and installs inside it
- `scripts/install-server.sh` — Linux/Debian server install (Docker + PostgreSQL + Redis + service)
- `scripts/install-local.ps1` — Windows all-in-one: Control Plane + Agent + Docker stack
- `scripts/install-agent.sh` — Linux agent (systemd service)
- `scripts/install-agent-freebsd.sh` — FreeBSD/OPNsense agent (rc.d service)
- `scripts/install-agent.ps1` — Windows agent (Windows Service)
- `scripts/build-release.ps1` — builds all platform binaries + MSI + EXE
- `build/windows/agent.wxs` — WiX MSI installer definition
- `build/windows/agent-installer.nsi` — NSIS EXE installer with config wizard
- `build/windows/local-installer.nsi` — NSIS all-in-one local installer

### Added — Security

- **Web authentication**: bcrypt admin password (cost 12), 8h sessions, HttpOnly + SameSite=Strict cookies
- **CSRF protection**: Double-Submit-Cookie pattern; agent routes and `/auth/login` exempt
- **Rate limiting**: token-bucket per IP — 5 req/min on auth endpoints, 20 req/s global
- **Security headers**: CSP, HSTS (1 year), X-Frame-Options DENY, Referrer-Policy, Permissions-Policy
- **Input protection**: parameterized queries (pgx), 1 MB body limit, 30s request timeout
- **Token security**: SHA-256 hashes only in DB, never plaintext in logs or responses
- `internal/security/` package: `IPRateLimiter`, `CSRFProtect`, `ClientIPHashed`
- `SECURITY.md`: public security policy with correct GDPR language

### Added — GDPR / DSGVO

- **Audit log** (migration 000011): append-only table, indexed by resource/actor/time
- **IP hashing**: SHA-256 8-byte hash stored instead of plaintext IP
- **PostgreSQL Row Security Policy** (migration 000013): app user may only INSERT + SELECT on `audit_log` — UPDATE/DELETE blocked at DB level even if application code is compromised
- **GDPR fields on systems** (migration 000012): `data_owner`, `retention_days`, `gdpr_note`
- `GET /v1/gdpr/systems/{id}/export` — Art. 20 data portability (JSON)
- `DELETE /v1/gdpr/systems/{id}/purge` — Art. 17 right to erasure (catalog data)
- `GET /v1/audit` — transparency endpoint
- `docs/security/`: threat-model, TOM, gdpr-notes, audit-log (operator-private, in .gitignore)

### Added — Developer Tooling

- Global skill system: `clean-code`, `ccd-wertesystem`, `security`, `dsgvo` skills in `~/.claude/`
- `Makefile`: all build targets including `build-agent-freebsd`, `installer-windows`, `release`
- TLS dev certificate generator (`internal/tools/gencert`)
- `docs/setup/SETUP_EN.md` + `SETUP_DE.md`: step-by-step setup guides
- `INSTALL.md`: comprehensive installation reference

### Fixed

- SPA handler: `filepath.Join` with absolute path dropped base directory — fixed with `strings.TrimPrefix`
- PostgreSQL password mismatch on re-runs — password now persisted in `/etc/opensourcebackup/.db_password`
- `docker compose exec` vs `docker exec` in non-interactive shell — switched to `docker exec`
- Go 1.19 on Debian apt (too old) — install script now installs Go 1.22.5 from go.dev if needed
- Windows UtimesNano error: restic exits code 1 when it cannot set timestamps on protected Windows directories but data was fully restored — treated as success
- TypeScript duplicate style keys in Systems.tsx and Agents.tsx
- Dashboard.tsx variable shadowing (`s` style object vs render callback)
- Vite `--silent` flag not supported in installed version — removed

### Known Limitations (v0.1.0)

- No RBAC — single global admin password (B_RBAC planned)
- TLS is opt-in, not enforced (R-03)
- CSP contains `unsafe-inline` (required for Vite/Google Fonts — R-01)
- GDPR purge deletes catalog data only; Restic repository content requires separate manual process
- No retention/prune automation (B_RET planned)
- No Prometheus metrics (B15 planned)

---

---

## [Deutsch]

---

## [Unveröffentlicht] — 2026-06-08

### Sicherheit — API-Härtung & CSP

- **Autorisierung API-weit gehärtet (RBAC).** DSGVO-Export/-Löschung und das
  Audit-Log sind jetzt **nur für Admins**; Enrollment-Token sowie Schreib-
  zugriffe auf Systems/Jobs/Snapshots sind rollengeschützt (Operator zum
  Anlegen/Ändern, Admin zum Löschen) — analog zum bestehenden Repository/Policy-
  Muster.
- **SSRF-Schutz für Webhook-URLs.** Webhook-Ziele werden vor Speichern und
  Versand validiert; private/Loopback/Link-Local- und Cloud-Metadaten-Bereiche
  (z. B. 169.254.169.254) sowie Nicht-HTTP(S)-Schemata werden abgelehnt.
- **Rate-Limiting auf Enrollment-Endpunkten** (`POST /v1/agent/enroll`,
  `POST /v1/systems/{id}/enrollment-token`) — 2/min pro IP.
- **CSRF-Token-Vergleich in konstanter Zeit** (`crypto/subtle`).
- **Content-Security-Policy gehärtet (R-01).** `script-src 'self'` — der Build
  erzeugt keine Inline-Scripts, daher `'unsafe-inline'` entfernt (XSS-Haupt-
  vektor zu). Tote Font-CDN-Origins entfernt (es werden keine Web-Fonts geladen).
  `style-src` behält `'unsafe-inline'` bewusst (React-Inline-Styles) — jetzt
  dokumentiert.
- **Keine rohen Store-Fehler mehr an Clients** — Sentinel-Fehler liefern sichere
  Meldungen, alles andere wird zu „internal error" (Details serverseitig
  geloggt).

### Hinzugefügt

- **Stale-Job-Reaper.** Laufende Backup-/Retention-Jobs, die verstummen, werden
  alle 10 min automatisch auf „failed" gesetzt — zweistufige Karenz (still nach
  Fortschritt 30 min, oder nie Fortschritt seit Start 12 h), damit ein
  abgestürzter Agent nie einen Job in „running" hängen lässt. `internal/scheduler`
  + `catalog.JobStore.FailStaleJobs`.
- **Email-/SMTP-Benachrichtigungen.** Echter SMTP-Versand (STARTTLS auf 587,
  implizites TLS auf 465); Credentials nur aus der Umgebung
  (`SMTP_HOST/FROM/PORT/USERNAME/PASSWORD/TLS`) — nie DB, nie argv, nie Logs.
  Empfänger-Validierung + CRLF-Header-Injection-Schutz. Settings-UI bietet den
  Email-Kanal an.
- **Pfad-Guardrails für Restore-Ziele (B_RESTORE_REPO_SELECT).** Das „New Restore
  Test"-Modal bewertet den Zielpfad (leer/sicher/Vorsicht/gefährlich) und
  blockiert Dateisystem-Wurzeln und geschützte System-Verzeichnisse. Advisory —
  die Agent-Prüfung `validateRestorePath` bleibt maßgeblich. Repository-Auswahl
  war bereits durchverdrahtet.
- **Cockpit-Redesign.** Operations-Zentrale — KPI-Karten (Systeme, laufende Jobs,
  Fehler, Restore-Verifikation), große Live-Status-Karten pro System mit
  Fortschritt/Durchsatz/Stop, Run-Now-Panel, Repository-Sidebar und schlanke
  Letzte-Jobs-Tabelle — statt reiner Tabelle. Kleine Komponenten unter
  `web/src/components/cockpit/`.
- **Frontend-Test-Runner (Vitest).** Das Web-Projekt hatte keinen; ergänzt
  Unit-Tests für `lib/pathSafety` und die Cockpit-Helfer + `test`-Script.
- **Pre-Push-Quality-Gate.** Versionierter Git-Hook (`scripts/hooks/pre-push`):
  Secret-/Privatdaten-Scan + `go vet`/`go test` + `tsc` + `vitest` bei jedem Push.

### Behoben

- **Scheduler feuerte nie (5-Feld-Cron).** `cron.New(cron.WithSeconds())`
  erwartete 6-Feld-Ausdrücke, während alle Policies 5-Feld nutzen — daher
  `scheduled_policies` = 0 und keine geplanten Backups. Jetzt `cron.New()`.
- **Backup-Fehler nicht diagnostizierbar.** `runBackup` verwarf restics stderr,
  Fehler kamen nur als „exit status N". stderr wird jetzt erfasst (begrenzt,
  8 KiB) und an den Fehler gehängt.
- **Restore-Pfad „/" rutschte durch.** Die Pfadprüfung normalisierte ein einzelnes
  „/" zu leer und fiel auf eine weiche Warnung; es gilt jetzt als Dateisystem-
  Wurzel (blockiert). Von einem Unit-Test gefunden.

## [Unveröffentlicht] — 2026-06-06

### Behoben — Agent-Zuverlässigkeit (Backups waren still stehen geblieben)

- **Restic-Binary-Auflösung (`exec.ErrDot`)** — Backups schlugen sofort fehl mit
  `restic init: exec: "restic": cannot run executable found relative to current directory`.
  Bei nicht gesetztem `RESTIC_BIN` rief der Agent restic mit dem bloßen Namen auf;
  Go's `os/exec` verweigert ein relativ zum Arbeitsverzeichnis aufgelöstes Binary.
  Der Agent löst restic jetzt auf einen **absoluten Pfad** auf (bevorzugt das Binary
  neben dem Agenten, sonst PATH-Lookup absolut gemacht). `internal/agent/restic/runner.go`
- **Agent-Autostart nur bei Login** — der Windows-Agent wurde über `HKCU\Run`
  gestartet und lief daher **nur bei angemeldetem User**; Backups blieben unbemerkt
  stehen, sobald niemand angemeldet war. Er installiert sich jetzt als echter
  **Windows-Dienst** (StartMode Auto, ohne Login, SCM-Restart bei Crash). Bei einem
  SMB/UNC-Repo läuft der Dienst **unter einem User-Konto** (LocalSystem hat kein
  Netzwerk-Credential); der Installer grantet diesem Konto das Recht *Als Dienst
  anmelden* per LSA. Lokal/Cloud nutzt LocalSystem.
- **Windows-Dienst startete nie (SCM)** — der Dienst-Start (ohne Argumente) landete
  im interaktiven Pfad statt in `svc.Run()`. Verzweigt jetzt über
  `service.Interactive()`, sodass sich der Dienst beim SCM registriert.
  `cmd/agent/main.go`
- **Backups tot nach Crash (verwaister Lock)** — ein Crash ließ den restic-Lock des
  vorigen Laufs liegen → jedes weitere Backup scheiterte mit `repository is already
  locked` (exit status 1), bis er von Hand entfernt wurde. Der Agent entfernt jetzt
  vor jedem Backup verwaiste Locks (`restic unlock`, nur stale — ein lebender Lock
  eines anderen Hosts am selben Repo bleibt erhalten). `internal/agent/restic/runner.go`
- **Agent als Dienst nicht beobachtbar** — Dienst-stdout wird verworfen; der Agent
  schreibt jetzt nach `AGENT_LOG_FILE`, damit Job-Logs erhalten bleiben. `cmd/agent/main.go`
- **`install-agent.ps1` crashte auf Windows PowerShell 5.1** — nutzte den nur in
  PS7 vorhandenen `??`-Operator; ersetzt, damit der `irm … | iex`-Einzeiler auf
  Standard-Windows funktioniert.

### Hinzugefügt

- **B_JOB_CANCEL** — Operator-Stop für ein laufendes Backup (Betriebssicherheit):
  eine wichtige Maschine schützen, ohne restic brutal zu killen. `POST /v1/jobs/{id}/cancel`
  markiert den Job (auditiert: wer/wann/warum); der Watcher des Agents bricht restic
  per Context ab und meldet den Job als **`cancelled`** (nicht `failed`). Operator-Rolle;
  der verwaiste Lock heilt via Auto-Unlock. Migration 000022.
- **B_JOB_PROGRESS** — Live-Backup-Fortschritt: der Agent streamt restics
  Fortschritt (Prozent, Bytes, Dateien, Durchsatz) an die Control Plane via
  `PUT /v1/agent/jobs/{id}/progress`; `GET /v1/jobs` liefert ihn. Gedrosselt auf
  ~2s, Durchsatz agent-seitig berechnet. **Privacy:** nur Aggregat-Zahlen werden
  gespeichert — restics `current_files` (Pfade) wird nie weitergereicht. Ein
  abgeschlossener Job wird auf 100% fixiert. Migration 000021. Grundlage fürs Cockpit.
- **B_LOWPRIO_RESTIC** — vom Agent gestartetes restic läuft unter Windows mit
  niedrigerer CPU-Priorität (`BELOW_NORMAL_PRIORITY_CLASS`), damit ein langes
  Backup interaktiver Arbeit weicht; No-op auf Linux/FreeBSD. Abschaltbar per
  `AGENT_LOW_PRIORITY_RESTIC=false` (Default an). Senkt nur CPU-Konkurrenz —
  kein Versprechen, Crashes zu verhindern.
- **Restic ohne Passwort** — `--insecure-no-password` bei Verify und dem generischen
  restic-Aufruf, wenn kein Passwort gesetzt ist; `init --quiet` für saubere Logs.

### Sicherheit / Aufräumen

- **`.gitignore` gehärtet** — schließt alle privaten/sensiblen Daten aus: `certs/`
  komplett (+ global `*.key/*.pem/*.crt/*.p12/*.pfx`), `.env` (`.env.example` bleibt),
  `data/` (Restore-Sandboxes), `tooldesign/` und `osb-server-*` Build-Artefakte.

---

## [Unveröffentlicht] — 2026-06-01

### Hinzugefügt — Dashboard & UI

- **B_DASH** — Dashboard v2: KPI-Zeile, Recovery Score mit erklärten Abzügen,
  Restore Verification Donut (SVG, keine externe Library), Quick Actions in Sidebar
- **Activity Chart** — 24h-Balkendiagramm (Backups / Restore Tests / Failures), SVG
- **Alerts Preview Panel** — Top-Alerts direkt im Dashboard mit Severity und Handlungsempfehlung
- **Recent Evidence Panel** — Letzte 6 Audit-Events im Dashboard
- **Repository Health Tabelle** — Immutability-Badge, Verschlüsselung, Verified-Anzahl, Last Backup/Restore
- **Agent Activity** — Online/Idle/Offline Donut + Last-Seen-Liste mit Status-Punkten

### Hinzugefügt — Sicherheit & DSGVO

- **B_IMM** — Immutable Repository Checks: `immutable_mode`-Feld (none/object_lock/worm/append_only/unknown),
  Migration 000015, Repository Health API
- **B_AUD** — Strukturiertes Audit-Log: `actor_type` + `severity`-Felder (Migration 000016),
  Fluent Builder, Events in Repositories-, Policies-, Enrollment- und Retention-Handlern verdrahtet,
  Evidence-Seite mit Severity/Kategorie-Filter
- **B_RBAC** — Multi-User RBAC: `users`-Tabelle (Migration 000017), Admin/Operator/Viewer-Rollen,
  Bootstrap-Admin beim ersten Start (ADMIN_EMAIL + ADMIN_PASSWORD),
  Destructive Endpoints nach Rolle geschützt, Sessions bei Rollenwechsel invalidiert,
  Letzter-Admin-Schutz (kann nicht gelöscht oder degradiert werden)
- **B_TLS** — TLS-Härtung: `TLS_REQUIRED=true` verweigert HTTP-Start,
  HTTP→HTTPS Redirect-Server, `gencert` mit LAN-IP-Argumenten

### Hinzugefügt — Observability

- **B15** — Prometheus Metriken (`GET /metrics`): Jobs, Restore Tests, Agent-Status,
  Snapshot-Verifizierungsrate, Recovery Score — gleiche Wahrheit wie Dashboard
- **B_HB** — Agent Heartbeat: `PUT /v1/agent/heartbeat`, `systems.last_seen` bei jedem Poll,
  Agent Activity Donut, Last-Seen-Liste
- **B_SC v2** — Backup Health Score v2: kanonisches `internal/health/`-Package,
  `GET /v1/health/score`, `GET /v1/health/activity`, 12 Abzugscodes,
  Positive Faktoren neben Abzügen angezeigt
- **B_ALERTS** — Alerts-Seite (`/alerts`): zustandslose Alerts aus Health-Score-Abzügen,
  Severity-/Kategorie-Filter, Handlungsempfehlung pro Alert, Navigation zu betroffener Seite

### Hinzugefügt — Retention

- **B_RET** — Retention + Prune Engine: typisierte Retention-Felder auf Policies
  (Migration 000014), `backup_jobs.type` (backup | retention),
  **Harte Sicherheitsregel**: Letzter restore-getesteter Snapshot wird niemals gelöscht

### Hinzugefügt — Installer & DevOps

- **Proxmox LXC Auto-Installer** (`scripts/install-proxmox.sh`)
- **Windows Alles-in-einem Installer** (`scripts/install-local.ps1`, NSIS)
- **MSI-Installer** (`build/windows/agent.wxs`) für Enterprise SCCM/GPO
- **GitHub Action** auto-synct master→main nach jedem Push

### Behoben

- RBAC Redirect-Loop, Dev-Mode, CSRF-Token fehlte in POST/PUT/DELETE
- Vite base-Pfad, API-URL-Fallback, TypeScript-Fehler
- Alter `calcRecoveryScore` aus Dashboard entfernt (jetzt Go-Backend)

---

## [0.1.0] — 2026-05-31

### Hinzugefügt — Core Platform

- **Control Plane** (Go 1.22+): REST-API für Systeme, Repositories, Policies, Jobs, Snapshots und Restore-Tests
- **PostgreSQL 16** Katalog mit 13 Datenbank-Migrationen
- **Redis 7** für zukünftiges Caching/Pub-Sub
- **Scheduler**: Cron-basierte Policy-Ausführung, Dead-Man's-Switch für verpasste Backups
- **Snapshot-Katalog**: verfolgt jedes erfolgreiche Backup mit Größe, Dauer und Dateianzahl

### Hinzugefügt — Backup-Agent

- **Plattformübergreifende Agent-Binary** (eine Codebasis, vier Zielplattformen):
  - Windows x64 — läuft als Windows-Dienst
  - Linux x64 / ARM64 — läuft als systemd-Unit
  - FreeBSD x64 — läuft als rc.d-Dienst (OPNsense / pfSense)
- **Enrollment-Protokoll**: Einmal-Token → dauerhafter Agent-Token (SHA-256-gehasht in DB)
- **Restic Runner**: Backup, Restore, Restore-Test mit Sandbox-Isolierung
- **Windows UtimesNano-Fix**: Exit-Code 1 bei reinen Timestamp-Fehlern wird als Erfolg gewertet

### Hinzugefügt — Web-Dashboard

- **React 18 + TypeScript + Vite** SPA, direkt aus der Go-Binary ausgeliefert
- Seiten: Dashboard (Gesundheitsübersicht), Systeme, Agents, Repositories, Policies, Jobs, Restore-Tests
- **Job-Detail-Panel** mit Live-Aktualisierung alle 3 Sekunden bei aktiven Jobs
- **Enrollment-Wizard**: 4-Schritte-Assistent mit plattformspezifischen Installationsbefehlen
- 9 Repository-Typen mit Beschreibungen und Einrichtungshinweisen
- Engine-Auswahl: Restic, Borg, pgBackRest, Velero

### Hinzugefügt — Installer-Suite

- `scripts/install-proxmox.sh` — erstellt automatisch Debian-12-LXC-Container und installiert darin
- `scripts/install-server.sh` — Linux/Debian-Server-Installation (Docker + PostgreSQL + Redis + Dienst)
- `scripts/install-local.ps1` — Windows alles-in-einem: Control Plane + Agent + Docker-Stack
- `scripts/install-agent.sh` — Linux-Agent (systemd-Dienst)
- `scripts/install-agent-freebsd.sh` — FreeBSD/OPNsense-Agent (rc.d-Dienst)
- `scripts/install-agent.ps1` — Windows-Agent (Windows-Dienst)
- `scripts/build-release.ps1` — baut alle Plattform-Binaries + MSI + EXE
- `build/windows/agent.wxs` — WiX-MSI-Installer-Definition
- `build/windows/agent-installer.nsi` — NSIS-EXE-Installer mit Konfigurations-Assistent
- `build/windows/local-installer.nsi` — NSIS alles-in-einem lokaler Installer

### Hinzugefügt — Sicherheit

- **Web-Authentifizierung**: bcrypt Admin-Passwort (Cost 12), 8h Sessions, HttpOnly + SameSite=Strict Cookies
- **CSRF-Schutz**: Double-Submit-Cookie-Muster; Agent-Routen und `/auth/login` ausgenommen
- **Rate-Limiting**: Token-Bucket pro IP — 5 Anfragen/Min auf Auth-Endpunkten, 20 Anfragen/s global
- **Security-Header**: CSP, HSTS (1 Jahr), X-Frame-Options DENY, Referrer-Policy, Permissions-Policy
- **Eingabeschutz**: parametrisierte Queries (pgx), 1-MB-Body-Limit, 30s Request-Timeout
- **Token-Sicherheit**: nur SHA-256-Hashes in DB, niemals Klartext in Logs oder Antworten
- `internal/security/` Paket: `IPRateLimiter`, `CSRFProtect`, `ClientIPHashed`
- `SECURITY.md`: öffentliche Security-Policy mit korrekter DSGVO-Sprache

### Hinzugefügt — DSGVO

- **Audit-Log** (Migration 000011): Append-Only-Tabelle, indiziert nach Ressource/Akteur/Zeit
- **IP-Hashing**: SHA-256-8-Byte-Hash statt Klartext-IP
- **PostgreSQL Row Security Policy** (Migration 000013): App-User darf nur INSERT + SELECT auf `audit_log` — UPDATE/DELETE auf DB-Ebene gesperrt, auch bei kompromittiertem Anwendungscode
- **DSGVO-Felder an Systemen** (Migration 000012): `data_owner`, `retention_days`, `gdpr_note`
- `GET /v1/gdpr/systems/{id}/export` — Art. 20 Datenportabilität (JSON)
- `DELETE /v1/gdpr/systems/{id}/purge` — Art. 17 Recht auf Löschung (Katalogdaten)
- `GET /v1/audit` — Transparenz-Endpunkt
- `docs/security/`: Threat-Model, TOM, DSGVO-Hinweise, Audit-Log (in .gitignore, Betreiber-privat)

### Behoben

- SPA-Handler: `filepath.Join` mit absolutem Pfad überschrieb Basisverzeichnis — behoben mit `strings.TrimPrefix`
- PostgreSQL-Passwort-Konflikt bei Neuinstallationen — Passwort wird jetzt in `/etc/opensourcebackup/.db_password` dauerhaft gespeichert
- `docker compose exec` vs `docker exec` in nicht-interaktiver Shell — auf `docker exec` umgestellt
- Go 1.19 von Debian apt (zu alt) — Install-Script installiert jetzt Go 1.22.5 von go.dev
- Windows-UtimesNano-Fehler: Restic beendet sich mit Code 1 bei Timestamp-Fehlern auf geschützten Windows-Verzeichnissen, obwohl Daten vollständig wiederhergestellt wurden — wird jetzt als Erfolg gewertet
- TypeScript-Duplikat-Style-Keys in Systems.tsx und Agents.tsx
- Dashboard.tsx Variable-Shadowing (`s` Style-Objekt vs. Render-Callback)
- Vite-Flag `--silent` in installierter Version nicht unterstützt — entfernt

### Bekannte Einschränkungen (v0.1.0)

- Kein RBAC — globales Admin-Passwort (B_RBAC geplant)
- TLS ist opt-in, nicht erzwungen (R-03)
- CSP enthält `unsafe-inline` (erforderlich für Vite/Google Fonts — R-01)
- DSGVO-Purge löscht nur Katalogdaten; Restic-Repository-Inhalte erfordern separaten manuellen Prozess
- Keine Retention/Prune-Automatisierung (B_RET geplant)
- Keine Prometheus-Metriken (B15 geplant)
