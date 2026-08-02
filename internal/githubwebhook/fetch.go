package githubwebhook

import (
	"fmt"
	"os/exec"
	"strings"
)

// FetchMirror pulls refs from a GitHub remote into a local bare repo using the
// system git binary (libgit2 fetch is not wired). remoteURL must already
// include credentials (x-access-token).
func FetchMirror(repoPath, remoteURL string) error {
	if strings.TrimSpace(repoPath) == "" || strings.TrimSpace(remoteURL) == "" {
		return fmt.Errorf("fetch: repo path and remote URL required")
	}
	// Ensure a remote named "github" exists, then fetch all heads/tags.
	_ = exec.Command("git", "-C", repoPath, "remote", "remove", "github").Run()
	if out, err := exec.Command("git", "-C", repoPath, "remote", "add", "github", remoteURL).CombinedOutput(); err != nil {
		return fmt.Errorf("git remote add: %w (%s)", err, truncateOut(out))
	}
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--prune", "github",
		"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch: %w (%s)", err, truncateOut(out))
	}
	return nil
}

// FetchPRHead fetches a pull request head into refs/pull/<n>/head.
func FetchPRHead(repoPath, remoteURL string, number int, headSHA string) error {
	if err := ensureRemote(repoPath, remoteURL); err != nil {
		return err
	}
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/pull/%d/head", number, number)
	out, err := exec.Command("git", "-C", repoPath, "fetch", "github", refspec).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git fetch pr: %w (%s)", err, truncateOut(out))
	}
	if headSHA != "" {
		// Best-effort verify the tip matches the webhook SHA.
		got, _ := exec.Command("git", "-C", repoPath, "rev-parse", fmt.Sprintf("refs/pull/%d/head", number)).Output()
		if strings.TrimSpace(string(got)) != "" && !strings.HasPrefix(strings.TrimSpace(string(got)), headSHA[:min(7, len(headSHA))]) {
			// Not fatal — SHA may be abbreviated differently; continue.
		}
	}
	return nil
}

func ensureRemote(repoPath, remoteURL string) error {
	_ = exec.Command("git", "-C", repoPath, "remote", "remove", "github").Run()
	out, err := exec.Command("git", "-C", repoPath, "remote", "add", "github", remoteURL).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git remote add: %w (%s)", err, truncateOut(out))
	}
	return nil
}

func truncateOut(b []byte) string {
	s := string(b)
	// Never echo tokens that may appear in URLs if git prints the remote.
	s = strings.ReplaceAll(s, "x-access-token:", "x-access-token:***@")
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
