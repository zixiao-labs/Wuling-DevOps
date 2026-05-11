package sshd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/gliderlabs/ssh"
	"github.com/google/uuid"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
)

// Context keys for values set by publicKeyHandler. Each is its own type to
// avoid collisions with anything else that might pin values into ssh.Context.
type (
	ctxKeyUserID   struct{}
	ctxKeyUsername struct{}
	ctxKeyKeyID    struct{}
)

// gitService is one of the two git-server subcommands we accept.
type gitService string

const (
	uploadPack  gitService = "git-upload-pack"
	receivePack gitService = "git-receive-pack"
)

func (g gitService) needsWrite() bool { return g == receivePack }
func (g gitService) sub() string      { return strings.TrimPrefix(string(g), "git-") }

// parseGitCommand validates `rawCmd` from the SSH session and returns the
// detected service plus the resolved repo "path" (org/project/repo, no
// trailing .git). Returns an error on anything that isn't an exact
// git-upload-pack/git-receive-pack invocation against a single quoted path.
//
// We deliberately do not invoke a shell — we parse the command ourselves so
// "git-receive-pack 'foo'; rm -rf /" is treated as garbage rather than as
// a chained shell command.
func parseGitCommand(rawCmd string) (gitService, string, error) {
	cmd := strings.TrimSpace(rawCmd)
	if cmd == "" {
		return "", "", apperr.New(apperr.CodeBadRequest, "missing git command")
	}
	for _, svc := range []gitService{uploadPack, receivePack} {
		prefix := string(svc) + " "
		if strings.HasPrefix(cmd, prefix) {
			arg := strings.TrimSpace(strings.TrimPrefix(cmd, prefix))
			repoPath, err := unquoteArg(arg)
			if err != nil {
				return "", "", err
			}
			repoPath = strings.TrimPrefix(repoPath, "/")
			repoPath = strings.TrimSuffix(repoPath, ".git")
			if repoPath == "" {
				return "", "", apperr.New(apperr.CodeBadRequest, "empty repository path")
			}
			return svc, repoPath, nil
		}
	}
	return "", "", apperr.New(apperr.CodeBadRequest, "command not allowed (only git-upload-pack / git-receive-pack)")
}

// unquoteArg strips a single pair of single quotes (the form git uses when
// passing a path argument) and rejects anything that isn't a slug-safe path
// character — `[A-Za-z0-9_/.-]`. Real org/project/repo paths only ever use
// that set, and refusing the rest here is defense in depth even though we
// pass arguments to exec.Command directly (no shell expansion).
func unquoteArg(arg string) (string, error) {
	body := arg
	if len(arg) >= 2 && arg[0] == '\'' && arg[len(arg)-1] == '\'' {
		body = arg[1 : len(arg)-1]
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '/', c == '.', c == '_', c == '-':
			// allowed
		default:
			return "", apperr.New(apperr.CodeBadRequest, "invalid characters in repository path")
		}
	}
	return body, nil
}

// splitRepoPath turns "org/project/repo" into its three slugs. Anything else
// is rejected.
func splitRepoPath(p string) (orgSlug, projectSlug, repoSlug string, err error) {
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", apperr.New(apperr.CodeBadRequest, "path must be <org>/<project>/<repo>[.git]")
	}
	return parts[0], parts[1], parts[2], nil
}

// handleSession is the entry point the gliderlabs/ssh server invokes once
// auth has succeeded. It parses the command, authorises, and spawns the
// matching git subprocess wired to the SSH channel.
func (s *Server) handleSession(sess ssh.Session) {
	rawCmd := strings.Join(sess.Command(), " ")
	// gliderlabs gives us Command() as already-split argv; reconstruct so
	// our parser can apply the same single-quote rules whether the client
	// sent a single string or pre-split tokens.
	if rawCmd == "" {
		rawCmd = sess.RawCommand()
	}
	svc, path, err := parseGitCommand(rawCmd)
	if err != nil {
		writeErrAndExit(sess, err)
		return
	}
	orgSlug, projSlug, repoSlug, err := splitRepoPath(path)
	if err != nil {
		writeErrAndExit(sess, err)
		return
	}
	ctx := sess.Context()
	userID, _ := ctx.Value(ctxKeyUserID{}).(uuid.UUID)

	repo, projectID, orgID, err := s.deps.Store.ResolveRepoPath(ctx, orgSlug, projSlug, repoSlug)
	if err != nil {
		writeErrAndExit(sess, err)
		return
	}

	// Authorise. Membership is mandatory for write; reads also require
	// membership unless the repo is public.
	role, rerr := s.deps.Store.MemberRole(ctx, orgID, userID)
	if rerr != nil {
		writeErrAndExit(sess, rerr)
		return
	}
	if svc.needsWrite() {
		if role == "" {
			writeErrAndExit(sess, apperr.Forbidden("write access required"))
			return
		}
	} else {
		if role == "" && repo.Visibility != "public" {
			// Hide existence — return NotFound rather than Forbidden to match
			// the HTTP smart-transport handler's behaviour.
			writeErrAndExit(sess, apperr.NotFound("repo"))
			return
		}
	}

	repoPath := s.deps.Layout.Path(orgID, projectID, repo.ID)

	bin := s.deps.GitBinary
	if bin == "" {
		bin = "git"
	}
	// Empty env to keep the host environment from leaking; only PATH retained.
	// Matches internal/githttp/handler.go::gitCommand.
	cmd := exec.CommandContext(ctx, bin, svc.sub(), repoPath)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8"}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeErrAndExit(sess, apperr.Internal(err))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeErrAndExit(sess, apperr.Internal(err))
		return
	}
	cmd.Stderr = sess.Stderr()

	if err := cmd.Start(); err != nil {
		writeErrAndExit(sess, apperr.Internal(err))
		return
	}

	// Wire SSH channel <-> git subprocess. Two copy goroutines, plus the
	// main goroutine waiting for the process to exit. Closing stdin signals
	// EOF to git's parser — we have to do it explicitly because the SSH
	// channel doesn't propagate EOF when the client uploads its packfile.
	copyDone := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(stdin, sess)
		_ = stdin.Close()
		copyDone <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(sess, stdout)
		copyDone <- struct{}{}
	}()

	werr := cmd.Wait()
	// Drain copies so we don't leak goroutines if Wait returns first.
	<-copyDone
	<-copyDone

	if werr != nil {
		_ = sess.Exit(1)
		s.deps.Log.Warn("ssh git subprocess failed",
			"service", svc, "repo_path", repoPath, "err", werr)
		return
	}

	// Same post-push hooks as the HTTP path: mark non-empty + kick the index.
	if svc == receivePack {
		bgCtx := context.Background()
		if err := s.deps.Store.MarkRepoNotEmpty(bgCtx, repo.ID); err != nil {
			s.deps.Log.Warn("ssh mark repo non-empty failed",
				"repo_id", repo.ID, "err", err)
		}
		if s.deps.Indexer != nil {
			s.deps.Indexer.IndexAsync(repo.ID, repoPath)
		}
	}
	_ = sess.Exit(0)
}

// writeErrAndExit renders an apperr-style message on the SSH stderr channel
// and exits with a non-zero status. The single-line format matches what an
// `ssh git@host ...` user would see from openssh-server.
func writeErrAndExit(sess ssh.Session, err error) {
	e := apperr.As(err)
	if e == nil {
		_, _ = fmt.Fprintf(sess.Stderr(), "wuling-sshd: internal error\n")
		_ = sess.Exit(1)
		return
	}
	_, _ = fmt.Fprintf(sess.Stderr(), "wuling-sshd: %s\n", e.Message)
	_ = sess.Exit(httpStatusToExit(e))
}

// httpStatusToExit maps apperr HTTP statuses to non-zero exit codes so a
// scripted client can distinguish "not found" from "forbidden" if it cares.
func httpStatusToExit(e *apperr.Error) int {
	switch e.HTTPStatus() {
	case 401:
		return 2
	case 403:
		return 3
	case 404:
		return 4
	case 400:
		return 5
	default:
		return 1
	}
}
