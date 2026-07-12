package agent

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// agentEnv returns the environment for a spawned agent process: the daemon's
// own environment, but with PATH extended by the user's real login-shell PATH
// (so nvm / homebrew / ~/.local/bin binaries resolve) and TERM set for the PTY.
//
// Why this exists — "what happened with codex": Ultraflow's daemon runs under
// launchd with a bare, hand-maintained PATH, so `os.Environ()` misses the dirs
// where the agent CLIs actually live. The real `codex`/`node` sit under nvm
// (`~/.nvm/versions/node/*/bin`), which the plist PATH never listed, so a codex
// launch died with `exit 127`: the Superset `codex` shim (first on PATH) walks
// the rest of PATH for a real binary, finds none, and bails. Both Superset and
// Orca — the two mature parallel-agent runners — solve this by resolving the
// user's login-shell PATH and injecting it into every spawned agent. We do the
// same, so the launch environment tracks the user's real shell instead of a
// stale hardcoded list. See spec/agent-env.md.
func agentEnv() []string {
	env := os.Environ()
	if login := loginShellPATH(); login != "" {
		env = replaceEnv(env, "PATH", mergedPATH(os.Getenv("PATH"), login))
	}
	return append(env, "TERM=xterm-256color")
}

var (
	loginPATHOnce sync.Once
	loginPATH     string
)

// loginShellPATH resolves the user's real PATH by asking their login shell,
// cached after the first call (the shell/rc files don't change under us).
//
// It runs an INTERACTIVE login shell (`-ilc`), not a plain login shell (`-lc`),
// because version managers like nvm are commonly initialized only in the
// interactive block of ~/.zshrc — verified here: `zsh -lc` does not surface
// nvm's `codex`, `zsh -ilc` does. Orca uses the same `/bin/zsh -i -l -c` probe.
//
// The catch (Orca issue stablyai/orca#5657): an interactive-login probe can
// hang on managed Macs whose Endpoint Security agents must authorize every
// forked subprocess (oh-my-zsh, compinit, nvm…). Orca's recommended-but-not-
// yet-shipped mitigation is a timeout with a fallback; we adopt it up front —
// the probe is bounded, and on timeout/error we return "" so agentEnv keeps the
// ambient PATH. A hang costs a few seconds once, never the daemon.
func loginShellPATH() string {
	loginPATHOnce.Do(func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// `printf %s "$PATH"` — no trailing newline, no extra subprocess.
		out, err := exec.CommandContext(ctx, shell, "-ilc", `printf %s "$PATH"`).Output()
		if err != nil {
			return // leave loginPATH empty → caller keeps the ambient PATH
		}
		loginPATH = strings.TrimSpace(string(out))
	})
	return loginPATH
}

// mergedPATH puts the login-shell PATH first, then appends any dirs from the
// ambient PATH not already present, deduplicated. This gains the login dirs
// (nvm, homebrew, ~/.local/bin) without dropping anything the daemon's own
// PATH had — so the merge is strictly additive and order-stable.
func mergedPATH(ambient, login string) string {
	seen := make(map[string]bool)
	var dirs []string
	add := func(list string) {
		for _, d := range strings.Split(list, ":") {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	add(login)
	add(ambient)
	return strings.Join(dirs, ":")
}

// replaceEnv sets key=val in a KEY=VALUE env slice, replacing the existing
// entry if present or appending it otherwise.
func replaceEnv(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
}
