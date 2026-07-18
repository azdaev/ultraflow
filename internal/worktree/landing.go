package worktree

import (
	"fmt"
	"os/exec"
	"strings"
)

// LandPR lands a task's branch through a GitHub pull request instead of a local
// merge, so the human's checkout is never touched (model.LandingPR): push the
// branch, ensure a PR exists, and try to merge it. Like Rebase, `out` carries
// git/gh's own explanation when something soft goes wrong:
//   - merged == true: the PR landed (or an earlier attempt's PR was already
//     merged from the GitHub side — a retry counts that as success).
//   - merged == false, err == nil: the PR exists at `url` but GitHub wouldn't
//     merge it yet (failing checks, branch protection, conflicts) — out says
//     why, and the human can merge it there or retry later.
//   - err != nil: the landing itself broke (push or PR creation failed).
func (m *Manager) LandPR(taskID, title string) (url string, merged bool, out string, err error) {
	branch := branchFor(taskID)
	wtPath := m.pathFor(taskID)
	if cerr := commitPending(wtPath, title); cerr != nil {
		return "", false, cerr.Error(), cerr
	}
	// --force-with-lease: a rework or rebase after a failed attempt rewrites the
	// branch, but commits pushed by anyone else are never clobbered.
	if pout, perr := run(wtPath, "push", "--force-with-lease", "-u", "origin", branch); perr != nil {
		return "", false, pout, fmt.Errorf("git push: %w: %s", perr, pout)
	}

	// Reuse the PR from an earlier attempt; view failing just means none exists.
	if state, u, verr := m.prView(wtPath, branch); verr == nil && u != "" {
		if state == "MERGED" {
			return u, true, "", nil
		}
		url = u
	}
	if url == "" {
		cout, cerr := gh(wtPath, "pr", "create", "--fill", "--head", branch)
		if cerr != nil {
			return "", false, cout, fmt.Errorf("gh pr create: %w: %s", cerr, cout)
		}
		// gh prints the new PR's URL as the last stdout line.
		if lines := strings.Fields(cout); len(lines) > 0 {
			url = lines[len(lines)-1]
		}
	}

	if mout, merr := gh(wtPath, "pr", "merge", "--merge", branch); merr != nil {
		return url, false, mout, nil
	}
	return url, true, "", nil
}

// prView returns the state (OPEN/MERGED/CLOSED) and url of the branch's PR, if
// one exists.
func (m *Manager) prView(wtPath, branch string) (state, url string, err error) {
	out, err := gh(wtPath, "pr", "view", branch, "--json", "state,url", "--jq", `.state + " " + .url`)
	if err != nil {
		return "", "", err
	}
	state, url, _ = strings.Cut(out, " ")
	return state, url, nil
}

// gh runs the GitHub CLI inside dir, returning trimmed combined output (same
// contract as run for git).
func gh(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
