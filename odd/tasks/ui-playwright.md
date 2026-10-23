# Automated Playwright tests for the monitoring UI

Browser tests for `ui/` (index.html + app.js) against a deterministic fake backend, run in CI.
Branch `test/ui-playwright`, stacked on `feat/client-follow-up` (PR #113); separate PR.

## Specs

- **S1** — User request (verbatim): "En un worktree distinto armemos tests automaticos con
  playwright para el front, pero no en este pr." Worktree
  `../ai-solo-startup-framework-playwright`, branch `test/ui-playwright`.
- **S2** — Base (user decision): "Desde feat/client-follow-up": the tests cover the follow-up UI
  ("Request changes") as well as the existing UI.
- **S3** — CI (user decision): "Sí, job nuevo en ci.yml": a new job installs Node + Chromium and
  runs the suite on every PR, blocking like `go test`.
- **S4** — Deterministic backend: the browser talks to the real `ui.UIHandler` (real routes, real
  embedded static files) backed by an in-memory fake `ui.Supervisor` with seeded tasks and a reset
  endpoint, so no provider CLI or network is needed.
- **S5** — Covered behaviors: sending a task; approve/reject on an `INPUT_REQUIRED` card;
  delegation nesting; "Request changes" shown exactly on `can_follow_up` cards; a submitted
  follow-up appears nested under its task; a refused follow-up shows a failure toast; typed text
  in an open follow-up form survives an SSE re-render.

## Tasks

- [x] T1 — S4: fake backend `ui/e2e/fakeserver` — worker — commit:
- [x] T2 — S1, S2, S5: Playwright project and specs in `ui/e2e` — worker — commit:
- [x] T3 — S3: CI job in `.github/workflows/ci.yml`, README and `.gitignore` — worker — commit:

## Log

- **L1** — "En un worktree distinto armemos tests automaticos con playwright para el front, pero no
  en este pr."
- **L2** — Decisions: "Desde feat/client-follow-up"; "Sí, job nuevo en ci.yml".
- **L3** — T1–T3 implemented (uncommitted). Fake backend `ui/e2e/fakeserver` (+ Go rules test);
  Playwright 1.64.1 (chromium v1248) with 8 specs in `ui/e2e/tests/ui.spec.ts` covering S5; CI job
  `ui-e2e` (S3). Refused follow-up is produced via `POST /test/set-state` (no broadcast → stale
  browser view). Evidence: `npx playwright test` → 8 passed; `go vet ./...`, `go test ./...` green.
- **L4** — Parent verification: `go test ./ui/... -race` ok; `npx playwright test` 8 passed;
  mutation (`const followUp = true` in app.js) makes spec 5 fail, so eligibility is pinned.
  PR #113 merged (e8aed11); this branch rebased onto master.
- **L5** — User: "agrega cache de playwright para reducir el timpoe del ci". Evidence (PR #94 run
  39991374879): `playwright install --with-deps` spent 8m12s in apt ("Fetched 25.9 MB in 8min 12s
  (52.7 kB/s)") and ~10s downloading browsers, so caching `~/.cache/ms-playwright` alone would
  save ~10s. User chose "Contenedor oficial de Playwright (recomendado)": the `ui-e2e` job runs in
  `mcr.microsoft.com/playwright:v1.64.1-noble` (`--user 1001`), the install step is gone, and a
  first step fails when the image tag drifts from `@playwright/test` in `package-lock.json`
  (checked locally for both match and drift).
