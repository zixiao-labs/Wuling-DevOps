package authhttp

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// SSHKeyHandler wires the /api/v1/auth/ssh-keys subroutes. Defined as a
// separate type from Handler so the existing register/login/PAT handler
// stays focused; both are mounted under the same /auth group from
// server.New.
type SSHKeyHandler struct {
	Store    *userstore.Store
	Verifier *auth.Verifier
}

// Mount registers routes on r (which should already be the /auth subrouter).
// All routes require an authenticated identity.
func (h *SSHKeyHandler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Get("/ssh-keys", h.list)
		r.Post("/ssh-keys", h.create)
		r.Delete("/ssh-keys/{key_id}", h.delete)
	})
}

type createSSHKeyReq struct {
	Title     string `json:"title"      validate:"required,min=1,max=128"`
	PublicKey string `json:"public_key" validate:"required,min=1,max=8192"`
}

func (h *SSHKeyHandler) create(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	var req createSSHKeyReq
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// ParseAuthorizedKey accepts the same authorized_keys / id_*.pub format
	// users paste from `cat ~/.ssh/id_ed25519.pub`. It returns the key plus
	// options/comment — we ignore the latter (we have our own title) and
	// re-serialise just the canonical key + type so what's stored is exactly
	// what we'd later hand back to OpenSSH.
	key, _, _, _, perr := gossh.ParseAuthorizedKey([]byte(req.PublicKey))
	if perr != nil {
		httpapi.RenderError(w, r,
			apperr.Validation("invalid public key (expected OpenSSH format)", nil))
		return
	}
	canonical := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(key)))
	fp := gossh.FingerprintSHA256(key)
	k, err := h.Store.CreateSSHKey(r.Context(), userstore.CreateSSHKeyParams{
		UserID:      id.UserID,
		Title:       strings.TrimSpace(req.Title),
		Fingerprint: fp,
		PublicKey:   canonical,
	})
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, k)
}

func (h *SSHKeyHandler) list(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	keys, err := h.Store.ListSSHKeysForUser(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ssh_keys": keys})
}

func (h *SSHKeyHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	keyIDStr := chi.URLParam(r, "key_id")
	keyID, perr := uuid.Parse(keyIDStr)
	if perr != nil {
		httpapi.RenderError(w, r, apperr.New(apperr.CodeBadRequest, "invalid key id"))
		return
	}
	if err := h.Store.DeleteSSHKey(r.Context(), id.UserID, keyID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
