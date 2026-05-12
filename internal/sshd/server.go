// Package sshd implements the embedded Git-over-SSH transport.
//
// The server listens on its own port (default :2222) and only ever exec's
// `git-upload-pack` or `git-receive-pack` against the resolved bare repo —
// it is explicitly not a shell. Auth is by SSH public key registered via
// the /api/v1/auth/ssh-keys REST surface.
//
// Authorization mirrors the HTTP smart-transport handler in internal/githttp:
//   - upload-pack (read): member, or public repo when no identity attached
//   - receive-pack (write): member only
// PAT scopes don't apply here — possession of the private key is the
// credential; per-key scopes are a Stage-2 concern.
package sshd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/gliderlabs/ssh"
	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// CommitIndexer is the narrow surface this package needs from insightstore.
// Defining it locally keeps sshd free of the insightstore import; tests can
// drop in a no-op.
type CommitIndexer interface {
	IndexAsync(repoID uuid.UUID, repoPath string)
}

// Deps holds collaborators the sshd needs at session time.
type Deps struct {
	Cfg     config.SSHConfig
	Log     *slog.Logger
	Store   *userstore.Store
	Layout  *repostore.Layout
	Indexer CommitIndexer
	// GitBinary lets tests inject a fake; empty falls back to "git" on PATH.
	GitBinary string
}

// Server wraps gliderlabs/ssh's Server with our resolver/handler wiring.
type Server struct {
	deps Deps
	srv  *ssh.Server

	mu      sync.Mutex
	started bool
}

// New constructs a Server. Call Start to bind the listener.
func New(deps Deps) (*Server, error) {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Store == nil {
		return nil, errors.New("sshd: Store is required")
	}
	if deps.Layout == nil {
		return nil, errors.New("sshd: Layout is required")
	}
	s := &Server{deps: deps}

	hostSigner, err := LoadOrCreateHostKey(deps.Cfg.HostKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load host key: %w", err)
	}
	deps.Log.Info("sshd host key ready",
		"path", deps.Cfg.HostKeyPath,
		"fingerprint", gossh.FingerprintSHA256(hostSigner.PublicKey()),
	)

	s.srv = &ssh.Server{
		Addr:             deps.Cfg.Addr,
		Handler:          s.handleSession,
		PublicKeyHandler: s.publicKeyHandler,
		// We don't run subsystems (sftp etc.) and we never accept interactive
		// shells; explicitly turn off everything we don't use so behaviour
		// stays predictable.
		PtyCallback:           noPty,
		ChannelHandlers:       map[string]ssh.ChannelHandler{"session": ssh.DefaultSessionHandler},
		SessionRequestCallback: nil,
		MaxTimeout:            10 * time.Minute,
	}
	s.srv.AddHostKey(hostSigner)
	return s, nil
}

// Start binds the listener and serves in a background goroutine. Returns
// immediately. If binding fails the error comes back via errCh.
func (s *Server) Start(errCh chan<- error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	ln, err := net.Listen("tcp", s.deps.Cfg.Addr)
	if err != nil {
		errCh <- fmt.Errorf("ssh listen %s: %w", s.deps.Cfg.Addr, err)
		return
	}
	s.deps.Log.Info("ssh listening", "addr", s.deps.Cfg.Addr)
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			errCh <- err
		}
	}()
}

// Shutdown stops accepting new SSH connections and waits for in-flight
// sessions to finish (or ctx to expire). Calling Shutdown on a never-started
// server is a no-op.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		done <- s.srv.Shutdown(ctx)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = s.srv.Close()
		return ctx.Err()
	}
}

func noPty(_ ssh.Context, _ ssh.Pty) bool { return false }

// ----------------------------------------------------------------------------
// Auth
// ----------------------------------------------------------------------------

// publicKeyHandler resolves the offered key to a user_ssh_keys row and stashes
// the user identity into the session context for the Handler to read.
func (s *Server) publicKeyHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	fp := gossh.FingerprintSHA256(key)
	owner, err := s.deps.Store.ResolveSSHKeyByFingerprint(ctx, fp)
	if err != nil {
		s.deps.Log.Debug("ssh key not registered",
			"fingerprint", fp, "remote", ctx.RemoteAddr())
		return false
	}
	ctx.SetValue(ctxKeyUserID{}, owner.UserID)
	ctx.SetValue(ctxKeyUsername{}, owner.Username)
	ctx.SetValue(ctxKeyKeyID{}, owner.KeyID)
	// Best-effort: stamp last_used_at in the background.
	go s.deps.Store.TouchSSHKey(context.Background(), owner.KeyID)
	return true
}
