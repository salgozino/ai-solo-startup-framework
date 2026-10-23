# Example: software factory

A company whose product is software. You ask the CEO for an app; the team specifies,
designs, builds, tests, and security-reviews it, then the CEO notifies you on Telegram.

## The team

| Role | Prompt | Does |
|------|--------|------|
| `ceo` | [agents/ceo.md](agents/ceo.md) | Orchestrates the pipeline, one delegation per round; sends the delivery notice. |
| `product-owner` | [agents/product-owner.md](agents/product-owner.md) | Writes `docs/PRODUCT.md` with numbered acceptance criteria; delegates UX. |
| `ux-designer` | [agents/ux-designer.md](agents/ux-designer.md) | Writes `docs/UX.md`: flow, wireframes, exact copy, accessibility. |
| `tech-lead` | [agents/tech-lead.md](agents/tech-lead.md) | Writes `docs/ARCHITECTURE.md`: stack, layout, module API, test strategy. |
| `engineer` | [agents/engineer.md](agents/engineer.md) | Implements the code and unit tests; fixes defects. |
| `qa` | [agents/qa.md](agents/qa.md) | Verifies every acceptance criterion, in a real headless Chrome too; writes `docs/QA_REPORT.md`. |
| `security-reviewer` | [agents/security-reviewer.md](agents/security-reviewer.md) | Reviews the code; writes `docs/SECURITY_REVIEW.md`. |

## The pipeline

```
human ─▶ ceo ─▶ product-owner ─▶ ux-designer
             ─▶ tech-lead
             ─▶ engineer
             ─▶ qa
             ─▶ security-reviewer
             ─▶ (one fix cycle if needed: engineer ─▶ qa)
             ─▶ telegram_send + finish_task
```

The CEO delegates each phase directly instead of chaining them (tech-lead ─▶ engineer ─▶ qa):
a delegation's 10-minute timeout covers the peer's whole run, so a chain would make the
CEO's wait cover every phase below it.

All agents share the working directory the company starts from. Projects are written to
`workspace/<app-slug>/`.

## Browser verification for QA

The `qa` agent declares its own MCP server in `factory.yaml` (`mcp_servers`, pi only):
[chrome-devtools-mcp](https://github.com/ChromeDevTools/chrome-devtools-mcp) in headless,
isolated mode, run with `npx --offline`, so it must already be in your npx cache
(`npx -y chrome-devtools-mcp@latest --help` once, online). QA serves the project with
`python3 -m http.server` and drives the app through the browser tools.

Those browser tools are not action intents and do not pass through the risk policy:
declaring the server is your explicit grant to that agent.

## Risk policy

Nothing here needs human approval: `telegram_send` (CEO only) and `delegate_task` (CEO and
product owner) are both `safe`. Any other role calling them is hard-denied.

## Run the end-to-end test

```bash
export TELEGRAM_BOT_TOKEN=... TELEGRAM_OWNER_ID=...
./e2e.sh                      # default request: a tip calculator
./e2e.sh "Build a ..."        # or your own request
```

The script builds the binary, materializes `factory.yaml` in a fresh `.runs/<timestamp>/`
directory, sends the request through the monitoring UI's `/api/send`, waits for the CEO's
task to finish, and checks that:

- the CEO delegated to every pipeline role and the product owner delegated to UX,
- every task completed,
- every artifact exists and the QA/security reports end with a passing verdict,
- the QA report has a `## Browser verification` section against the served app,
- `node --test` passes in the delivered project,
- the Telegram notice was delivered.

The models in `factory.yaml` and the `pi-claude-bridge` path are the author's; adjust them to
your install.
