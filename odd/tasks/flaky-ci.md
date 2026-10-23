# Fix flaky CI: parked-task cancel and delegate state read

Master CI fails intermittently on two tests that passed on their PRs (#103, #105).
Branch: `fix/flaky-ci` in worktree `.worktrees/flaky-ci` (from master). Own PR.

## Specs

- S1 — User request, verbatim: "Che, CI esta roto, que pasa?" then decision "PR aparte desde
  master (recomendado)".
- S2 — `TestPark_ParkedCEOTaskIsCancellableWhilePeerRuns` (core/supervisor/park_integration_test.go:100)
  must pass deterministically. Its rule stands: a CEO task parked in INPUT_REQUIRED while its
  peer runs is cancellable — `CancelTask` succeeds and the task ends CANCELED. Today CancelTask
  fails with `cancelation failed: queue is closed` (cancel races the execution teardown) or
  `task was in non-cancelable state: "TASK_STATE_INPUT_REQUIRED": task cannot be canceled`
  (after the execution ended). Fix the product behavior at its source, not by retrying in the
  test. A canceled parked task must also stop its peer watcher and never resume later.
- S3 — `TestClient_DelegateDoesNotWaitForPeer` (transport/a2a/client_test.go:439) must pass
  deterministically: the peer may still be SUBMITTED right after Delegate returns; the test waits
  (bounded) for WORKING instead of reading once. Test-only fix.
- S4 — No other behavior changes; `go test ./... -count=1 -race` green, and both tests pass
  `-race -cpu 1,2 -count=100`.

## Tasks

- T1 [S2,S4] parked-task cancellation fix + test — gentle-ai-worker — commit: pending
- T2 [S3,S4] delegate state read waits for WORKING — gentle-ai-worker — commit: pending

## Log

- L1 (user): see S1.
- L2 (evidence, master 48b8cd1): `go test -race -cpu 1,2 -count=100` -> TestPark fails 68/200
  (21 "queue is closed", 47 "non-cancelable state INPUT_REQUIRED"); TestClient fails ~1-2/200
  ("peer state = TASK_STATE_SUBMITTED, want WORKING"). CI failures: run 39877708646 (#105 merge,
  TestPark), run 39873569841 (#103 merge, TestClient). CI command: `go test ./... -count=1 -race`.
