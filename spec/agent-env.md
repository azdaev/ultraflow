# Agent launch environment — "what happened with codex"

How Ultraflow builds the environment for a spawned agent process, why codex was
failing, and what we borrowed from the two mature parallel-agent runners
(Superset and Orca) to fix it.

## Symptoms (from `~/.ultraflow/ultraflow.log`)

Two distinct failure modes, often conflated:

1. **`agent exited before finishing: exit status 127`** — codex specifically.
   A hard "command/binary not found". This is the one this task fixes.
2. **`agent exited before finishing: signal: killed`** — many tasks, any agent.
   The process was killed (SIGKILL), not a missing binary. Separate issue; see
   "Still open" below.

## Root cause of the `exit 127`

`codex` on this machine is not a single binary — the PATH-first entry is a
**Superset shim** at `~/.superset/bin/codex` (a bash wrapper that adds
notify/trust hooks, then `exec`s the *real* codex). The shim finds the real
binary by walking `$PATH`, skipping its own dir:

```bash
# ~/.superset/bin/codex
find_real_binary() { for dir in $PATH; do ... skip .superset/bin ...; [ -x "$dir/codex" ] && echo "$dir/codex"; done }
REAL_BIN="$(find_real_binary codex)"
[ -z "$REAL_BIN" ] && { echo "Superset: codex not found in PATH"; exit 127; }
```

The real codex is installed under **nvm**: `~/.nvm/versions/node/*/bin/codex`
(a `#!/usr/bin/env node` script — so it also needs `node`, same dir). nvm's bin
dir only joins `$PATH` when nvm initializes, which happens in the **interactive**
block of `~/.zshrc`.

Ultraflow's daemon runs under **launchd** with a bare, hand-maintained PATH
(`deploy/com.ultraflow.daemon.plist`). That list had `~/.superset/bin` and
`~/.local/bin` but **not** the nvm dir. So a spawned codex hit the shim → the
shim walked a PATH with no real codex on it → `exit 127`. (claude survived only
because its real binary sits in `~/.local/bin`, which *was* on the list.)

The hardcoded plist PATH is the fragile part: machine-specific, hand-edited, and
it silently went stale the moment codex arrived via nvm.

## What Superset and Orca do (the best practice)

Both are open-source desktop apps that run many subscription-CLI agents in
parallel — the same problem Ultraflow has — and both solve the launch
environment the same way: **don't trust the app's own (GUI/launchd) env;
resolve the user's real login-shell PATH and inject it into every spawned
agent.**

- **Superset** (`github.com/superset-sh/superset`, Apache-2.0) ships the per-agent
  shim above. The shim *assumes* a correct PATH is already in the environment —
  i.e. the Electron app is responsible for having resolved and injected it before
  spawning the terminal. The shim is just discovery + hooks on top.
- **Orca** (`github.com/stablyai/orca`, MIT) resolves it explicitly. Its bundle
  runs the login shell and captures PATH:
  - probe: `/bin/zsh -i -l -c '… __ORCA_SHELL_PATH__ …'` through a PTY,
  - then `process.env.PATH = <resolved>`.

  Note the flags: **interactive login** (`-i -l`), not a plain login shell. We
  verified why that matters here: `zsh -lc` does **not** surface nvm's codex
  (nvm inits in the interactive rc block), while `zsh -ilc` **does**.

### The caveat Orca learned the hard way

Orca issue [stablyai/orca#5657](https://github.com/stablyai/orca/issues/5657):
an interactive-login probe evaluates the full `.zshrc`, which forks many
subprocesses (oh-my-zsh, `compinit`, nvm, completions). On managed Macs with
Endpoint Security agents (Jamf Protect, CrowdStrike) every spawn must be
authorized by the kernel, and the probe can **hang uninterruptibly at launch**.
Their recommended-but-not-yet-shipped mitigation: **bound the probe with a
timeout (2–3s) and fall back to a default PATH**, and run it async/cached.

## What Ultraflow does now (`internal/agent/env.go`)

`agentEnv()` is the single source of the spawn environment for every launch
path (claude/codex, interactive `Command`/`ResumeCommand` and headless `Run`):

- Resolve the user's PATH once via `$SHELL -ilc 'printf %s "$PATH"'`
  (interactive login — so nvm etc. are present), cached with `sync.Once`.
- **Adopt Orca's mitigation from the start:** the probe is bounded by a 3s
  `context` timeout; on timeout/error we return "" and keep the ambient PATH, so
  a hang costs a few seconds once, never the daemon.
- Merge: login-shell PATH first, then any ambient dirs not already present
  (`mergedPATH`) — strictly additive, so we gain nvm/homebrew without dropping
  anything launchd provided.

Result (verified end-to-end): under the daemon's bare PATH, `codex` now resolves
to `~/.nvm/versions/node/*/bin/codex` (the real binary) instead of dying at the
shim. The plist PATH (`deploy/…plist`) is now only a fallback for a timed-out
probe; its comment says so, and it now lists the nvm dir too.

## Still open — the `signal: killed` mode

Not a PATH problem and not fixed here. `signal: killed` is the OS/daemon killing
the agent — most likely **memory pressure from too many concurrent heavy TUI
agents** (the log shows `max concurrent agents set to 7`; each `claude`/`codex`
TUI is heavyweight), or the daemon being restarted (launchd `KeepAlive`) mid-run.
Directions, if it keeps biting: lower the default concurrency, watch RSS and
back off, and confirm process-group teardown on restart (already addressed in
commit `dbd8713`) isn't racing `RecoverInFlight` re-spawns.
