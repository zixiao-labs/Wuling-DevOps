// Package githubwebhook receives GitHub App webhook deliveries.
//
// The endpoint is unauthenticated at the JWT layer: authenticity is proven
// solely by HMAC-SHA256 over the raw body (X-Hub-Signature-256). See
// docs/github-integration.md.
package githubwebhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
)

// MaxBodyBytes caps webhook payloads. GitHub's documented max is well under
// this; keeping headroom avoids cutting off large push payloads.
const MaxBodyBytes = 5 << 20 // 5 MiB

// Handler serves POST /api/v1/webhooks/github.
type Handler struct {
	Secret string
	Store  *Store
	Log    *slog.Logger
	// Process, when non-nil, handles verified first-seen events other than
	// ping. MVP leaves this nil (unknown events → 202). Events branch wires it.
	Process func(ctx EventContext) error
}

// EventContext is the verified delivery handed to Process.
type EventContext struct {
	DeliveryID string
	Event      string
	Action     string
	Body       []byte
	Log        *slog.Logger
}

// Mount registers the webhook route on parent (expected: /api/v1).
func (h *Handler) Mount(parent chi.Router) {
	parent.Post("/webhooks/github", h.ServeHTTP)
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpapi.WriteJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "body unreadable"})
		return
	}

	sig := r.Header.Get(signatureHeader)
	if !VerifySignature(h.Secret, body, sig) {
		h.log().Warn("github-webhook: signature rejected")
		httpapi.WriteJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "invalid signature"})
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	action := peekAction(body)

	log := h.log().With(
		"delivery_id", deliveryID,
		"event", event,
		"action", action,
	)

	if h.Store != nil {
		claimed, cerr := h.Store.ClaimDelivery(r.Context(), deliveryID, event)
		if cerr != nil {
			log.Error("github-webhook: claim delivery failed", "err", cerr)
			httpapi.WriteJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "internal"})
			return
		}
		if !claimed {
			log.Info("github-webhook: duplicate delivery ignored")
			httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "duplicate": true})
			return
		}
	}

	if event == "ping" {
		log.Info("github-webhook: ping")
		httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if h.Process == nil {
		log.Info("github-webhook: event accepted (no processor)")
		httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true, "accepted": true})
		return
	}

	if err := h.Process(EventContext{
		DeliveryID: deliveryID,
		Event:      event,
		Action:     action,
		Body:       body,
		Log:        log,
	}); err != nil {
		log.Error("github-webhook: process failed", "err", err)
		// Release the claim so GitHub's redelivery can retry the work.
		if h.Store != nil {
			_ = h.Store.ReleaseClaim(r.Context(), deliveryID)
		}
		httpapi.WriteJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "process failed"})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func peekAction(body []byte) string {
	var envelope struct {
		Action string `json:"action"`
	}
	_ = json.Unmarshal(body, &envelope)
	return envelope.Action
}
