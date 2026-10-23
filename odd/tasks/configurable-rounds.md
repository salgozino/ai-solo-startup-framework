# Configurable round limit + QA verifies the documented run path

Two findings from the multi-round experiment (notes app, all 8 CEO rounds used).
Branch: `feat/configurable-rounds` in worktree `.worktrees/configurable-rounds` (from master).

## Specs

- S1 — User request, verbatim: "si arregla los 2" — referring to: (1) "El CEO queda justo en el
  límite de 8 rondas ... habría que hacer configurable el límite de rondas, por ejemplo con un
  `max_rounds` en company.yaml, y ajustar el prompt del CEO." and (2) "El QA prueba siempre por HTTP
  ... Su prompt debería pedirle verificar las instrucciones del README tal como están escritas."
- S2 — Optional per-agent `max_rounds` in company.yaml (any provider): the provider-round cap of
  that agent's task loop. Omitted -> 8 (today's behavior, byte-identical). Must be an integer from 1
  to 50; anything else fails `config.Load` with a named error naming the agent and the field.
  The supervisor uses it everywhere it uses `maxProviderRounds` today, including the round-limit
  failure text `supervisor: provider round limit (N) exhausted after delegation` with the
  configured N. README documents the field (agent config section).
- S3 — examples/software-factory: the CEO gets `max_rounds: 12` and its prompt states 12 rounds and
  allows up to two fix cycles while always reserving the last round for telegram_send + finish_task.
  The product-owner prompt's round mention stays accurate.
- S4 — examples/software-factory: QA verifies the project's README run instructions exactly as
  written (e.g. "open index.html directly" -> open the file:// URL in the browser); if they do not
  work as written (console errors, app not loading) that is a defect. The engineer's README must
  give run instructions that work as written (an ES-module app must be served over HTTP, or not
  use modules). Browser verification over HTTP stays.

## Tasks

- T1 [S2] config + core/supervisor + cmd/company + README: per-agent max_rounds, TDD — gentle-ai-worker — commit: pending
- T2 [S3,S4] examples/software-factory prompts + factory.yaml — inline — commit: pending
- T3 [S2-S4] e2e run of the example — inline — commit: n/a

## Log

- L1 (user): see S1.
- L2 (evidence): experiment run .runs/20261023-120007 — CEO rounds 1..8 used (fix cycle + QA and
  security re-verified in one turn in round 7, telegram+finish in round 8). Delivered app uses
  `<script type="module">` while its README says "Open `index.html` directly in a browser"; QA
  tested only over HTTP (its prompt mandates it), so the documented path was never checked.
- L3 (code): `maxProviderRounds = 8` at core/supervisor/supervisor.go:29, used at :566-567 and
  pinned by core/supervisor/park_test.go:218.
