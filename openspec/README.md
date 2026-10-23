# OpenSpec — frozen historical record

This directory is frozen as of **2026-10-02**. SDD (OpenSpec's spec-driven
flow) is retired in favor of ODD (Organic Driven Development), with feature
task documents in `odd/tasks/` mirrored to Engram under `odd/<feature-name>/tasks`.

- `openspec/specs/` remains a read-only reference for the behavior specified then.
- `openspec/changes/archive/` holds completed changes.
- `openspec/changes/agent-delegation-chained-approval/` remains a read-only design
  source for the chained-approval slice executed under ODD.
- `openspec/config.yaml` is retired and retained for history only.

Consult these files as historical design references only. Do not add new changes
or update this frozen record; track new feature work in `odd/tasks/<feature-name>.md`.
