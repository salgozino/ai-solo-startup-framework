"""Assertions for e2e.sh, reading the monitoring UI's /api/tasks snapshot.

  e2e_check.py status <tasks.json>                        -> one-line progress summary
  e2e_check.py verify <tasks.json> <workspace> <log> <browser-log>
                                                          -> exit 0 only if the pipeline passed
"""
import json
import pathlib
import subprocess
import sys

PIPELINE = ["product-owner", "tech-lead", "engineer", "qa", "security-reviewer"]
ARTIFACTS = [
    "docs/PRODUCT.md",
    "docs/UX.md",
    "docs/ARCHITECTURE.md",
    "docs/QA_REPORT.md",
    "docs/SECURITY_REVIEW.md",
    "README.md",
]
TERMINAL = ("COMPLETED", "FAILED", "REJECTED", "CANCELED")


def load(path):
    return json.loads(pathlib.Path(path).read_text())["tasks"]


def terminal(state):
    return any(t in state.upper() for t in TERMINAL)


def root_task(tasks):
    """The CEO task the human submitted (the CEO never receives delegations)."""
    ceo = [t for t in tasks if t.get("agent") == "ceo"]
    return ceo[0] if ceo else None


def status(tasks):
    root = root_task(tasks)
    if root is None:
        return "waiting: no CEO task yet"
    # A real human escalation carries the pending intent. A task resuming after a peer's
    # result runs its next round still in INPUT_REQUIRED with no awaiting_peer, so the
    # state alone is not enough.
    escalated = [t for t in tasks if "INPUT_REQUIRED" in t["state"].upper() and t.get("pending_intent_kind")]
    if escalated:
        return "escalated: " + ", ".join(f"{t['agent']}:{t.get('pending_intent_kind')}" for t in escalated)
    trail = " ".join(f"{t['agent']}={t['state'].split('_')[-1]}" for t in tasks)
    if terminal(root["state"]):
        return f"done: {trail}"
    return f"running: {trail}"


def browser_used(browser_log):
    """Evidence from chrome-devtools-mcp's own log that QA drove the served app."""
    path = pathlib.Path(browser_log)
    text = path.read_text(errors="replace") if path.is_file() else ""
    checks = {
        "navigated to a served http://127.0.0.1 URL": "http://127.0.0.1:" in text,
        "the browser resolved at least once": "browser: resolved" in text,
        "interacted with the page (fill/click/type/press)": any(
            f"mcp:log {tool} request" in text for tool in ("fill", "fill_form", "click", "type_text", "press_key")
        ),
    }
    return [name for name, ok in checks.items() if not ok]


def verify(tasks, workspace, log, browser_log):
    failures = []
    by_id = {(t["agent"], t["task_id"]): t for t in tasks}
    root = root_task(tasks)
    if root is None:
        print("FAIL: no CEO task")
        return 1
    print(f"CEO task {root['task_id']}: {root['state']}")
    if "COMPLETED" not in root["state"].upper():
        failures.append(f"CEO task ended {root['state']}: {root.get('output', '')[:500]}")

    ceo_roles = [d["role"] for d in root.get("delegations") or []]
    print("CEO delegations, in order:", " -> ".join(ceo_roles))
    for role in PIPELINE:
        if role not in ceo_roles:
            failures.append(f"CEO never delegated to {role}")

    po = [by_id.get((d["role"], d["task_id"])) for d in root.get("delegations") or [] if d["role"] == "product-owner"]
    po_roles = [d["role"] for t in po if t for d in t.get("delegations") or []]
    print("product-owner delegations:", " -> ".join(po_roles) or "(none)")
    if "ux-designer" not in po_roles:
        failures.append("product-owner never delegated to ux-designer")

    for t in tasks:
        if not terminal(t["state"]) or "COMPLETED" not in t["state"].upper():
            failures.append(f"{t['agent']} task {t['task_id']} ended {t['state']}")

    projects = [p for p in pathlib.Path(workspace).glob("*") if p.is_dir()]
    if len(projects) != 1:
        failures.append(f"expected exactly one project in {workspace}, found {[p.name for p in projects]}")
    for project in projects:
        print(f"project: {project}")
        for rel in ARTIFACTS:
            if not (project / rel).is_file():
                failures.append(f"missing artifact {project.name}/{rel}")
        for rel, verdict in (("docs/QA_REPORT.md", "QA VERDICT: PASS"), ("docs/SECURITY_REVIEW.md", "SECURITY VERDICT: APPROVED")):
            f = project / rel
            last = f.read_text().strip().splitlines()[-1] if f.is_file() and f.read_text().strip() else ""
            print(f"{rel} verdict: {last!r}")
            if last != verdict:
                failures.append(f"{rel} final line is {last!r}, want {verdict!r}")
        # QA must have driven a real browser against the served app, not only read the code.
        qa = project / "docs/QA_REPORT.md"
        qa_text = qa.read_text() if qa.is_file() else ""
        browser = qa_text.split("## Browser verification", 1)[1] if "## Browser verification" in qa_text else ""
        print("QA browser verification:", "present" if "http://127.0.0.1:" in browser else "missing")
        if "http://127.0.0.1:" not in browser:
            failures.append("docs/QA_REPORT.md has no '## Browser verification' section with a served http://127.0.0.1 URL")
        # QA must also have followed the project's README run instructions as written.
        if "README run instructions:" not in browser:
            failures.append("docs/QA_REPORT.md '## Browser verification' has no 'README run instructions:' line")
        run = subprocess.run(["node", "--test"], cwd=project, capture_output=True, text=True, timeout=300)
        # TAP reporter prints "# pass 3"; the default spec reporter prints "ℹ pass 3".
        summary = [l for l in run.stdout.splitlines() if l.lstrip("#ℹ ").startswith(("tests ", "pass ", "fail "))]
        print("node --test:", run.returncode, " ".join(summary))
        if run.returncode != 0:
            failures.append(f"node --test failed in {project.name}:\n{run.stdout[-2000:]}{run.stderr[-1000:]}")

    missing = browser_used(browser_log)
    print("QA browser log:", "ok" if not missing else "missing: " + "; ".join(missing))
    if missing:
        failures.append(f"no browser evidence in {browser_log}: " + "; ".join(missing))

    # The supervisor logs action.done only after the gateway delivered the message.
    log_lines = pathlib.Path(log).read_text().splitlines()
    if not any("action.done" in l and "telegram_send" in l for l in log_lines):
        failures.append("telegram_send was never executed (no action.done for it in the company log)")

    print()
    print("CEO final report:\n" + (root.get("output") or "(empty)"))
    print()
    if failures:
        print("E2E FAIL")
        for f in failures:
            print(" -", f)
        return 1
    print("E2E PASS: every role took part, all artifacts exist, verdicts pass, tests green, owner notified")
    return 0


if __name__ == "__main__":
    cmd = sys.argv[1]
    if cmd == "status":
        print(status(load(sys.argv[2])))
    elif cmd == "verify":
        sys.exit(verify(load(sys.argv[2]), sys.argv[3], sys.argv[4], sys.argv[5]))
    else:
        sys.exit(f"unknown command {cmd!r}")
