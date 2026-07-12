# Ultraflow

A local board to run many AI coding agents in parallel over your **own CLI
subscriptions** (Claude Code today; Codex/opencode planned) — no model API keys.

The core is a blocking MCP tool, `ask_human(question, options, context)`: when an
agent hits a decision it shouldn't guess, the call parks the agent and surfaces a
checkpoint on the board; you answer, and the agent continues. Everything else —
the kanban pipeline, git-worktree isolation per task, the attention rail — is
scaffolding around that loop.

It's a single-user local tool: your machine, your subscriptions, local SQLite. No
auth, no server, no multi-tenant.

## Install (macOS)

```sh
brew install azdaev/tap/ultraflow
```

Log into at least one agent CLI first (Ultraflow runs it headless, it never sees
your keys):

```sh
claude   # sign in once
```

Then:

```sh
ultraflow            # serves the board on http://localhost:7787
```

## Run it always (launchd)

```sh
mkdir -p ~/.ultraflow
cp "$(brew --prefix)/opt/ultraflow/deploy/com.ultraflow.daemon.plist" ~/Library/LaunchAgents/ 2>/dev/null \
  || curl -fsSL https://raw.githubusercontent.com/azdaev/ultraflow/main/deploy/com.ultraflow.daemon.plist -o ~/Library/LaunchAgents/com.ultraflow.daemon.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ultraflow.daemon.plist
```

Edit the plist's paths if you didn't install to the default Homebrew prefix. Stop
with `launchctl bootout gui/$(id -u)/com.ultraflow.daemon`.

## Build from source

```sh
make build     # frontend + a self-contained binary (frontend embedded)
./ultraflow
```

`make dev` runs against the on-disk frontend for fast iteration; `make test` runs
the race suite.

## Status

Early. The full `ask_human` loop, projects, git-worktree isolation, and both
board layouts work end to end. Only the Claude adapter and the Solo flow execute
today — other agents and the multi-step flows are shown but marked "· soon" until
the flow engine lands. See `spec/roadmap.md`.

## License

MIT — see [LICENSE](LICENSE).
