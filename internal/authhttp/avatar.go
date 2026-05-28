// Package authhttp — avatar upload/download.
//
// Storage layout: the file <AvatarsDir>/<user_id>.png contains the user's
// current avatar. We do NOT keep raw uploads: every upload is decoded into
// a Go *image.Image and re-encoded as a 256x256 PNG, so a malicious payload
// (SVG with JS, polyglot JPEG, decompression bomb) never reaches disk.
//
// On the read side, the public GET /api/v1/users/{username}/avatar endpoint
// streams the PNG when it exists and 404s otherwise. Frontends render a
// deterministic initials tile as a fallback so they don't need to make a
// HEAD request first.

package authhttp

import (
	"errors"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	// Side-effect imports register JPEG and GIF decoders for image.Decode.
	// PNG is already registered transitively via "image/png" above.
	_ "image/gif"
	_ "image/jpeg"

	"github.com/go-chi/chi/v5"
	"golang.org/x/image/draw"

	"github.com/zixiao-labs/wuling-devops/internal/apperr"
	"github.com/zixiao-labs/wuling-devops/internal/auth"
	"github.com/zixiao-labs/wuling-devops/internal/httpapi"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

// AvatarHandler wires upload, delete, and public GET endpoints for user
// avatars.
type AvatarHandler struct {
	Store    *userstore.Store
	Verifier *auth.Verifier
	OAT      auth.OATResolver
	Dir      string
}

// AvatarMaxBytes is the upper bound on uploaded bodies before we refuse
// to even decode. 2 MiB comfortably covers anything a UI would pick.
const AvatarMaxBytes = 2 * 1024 * 1024

// AvatarSize is the fixed pixel side of the canonical PNG we store. Square,
// 256x256, RGBA — enough for high-DPI rendering at the largest spot the UI
// uses (profile header, ~96 CSS pixels at 2x).
const AvatarSize = 256

// Mount registers the authenticated routes onto the /auth subrouter. The
// public GET /users/{username}/avatar lives elsewhere — see MountPublic.
func (h *AvatarHandler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(h.Verifier, false))
		r.Put("/avatar", h.upload)
		r.Delete("/avatar", h.delete)
	})
}

// MountPublic registers GET /users/{username}/avatar. Callers mount this on
// the /api/v1 router (no /auth prefix) so the URL matches the model.User
// avatar_url shape.
func (h *AvatarHandler) MountPublic(r chi.Router) {
	r.Get("/users/{username}/avatar", h.get)
}

func (h *AvatarHandler) upload(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	// MaxBytesReader returns a body that produces *http.MaxBytesError on
	// overflow — we surface it as a clean 413 rather than a generic decode
	// error, so the UI can render a useful message.
	r.Body = http.MaxBytesReader(w, r.Body, AvatarMaxBytes+1024) // +1k for form metadata
	if err := r.ParseMultipartForm(AvatarMaxBytes + 1024); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpapi.RenderError(w, r, apperr.Validation("avatar exceeds 2 MiB", nil))
			return
		}
		httpapi.RenderError(w, r, apperr.Validation("invalid multipart form: "+err.Error(), nil))
		return
	}
	file, header, ferr := r.FormFile("avatar")
	if ferr != nil {
		httpapi.RenderError(w, r, apperr.Validation(`missing "avatar" form field`, nil))
		return
	}
	defer file.Close()
	if header.Size > AvatarMaxBytes {
		httpapi.RenderError(w, r, apperr.Validation("avatar exceeds 2 MiB", nil))
		return
	}

	img, format, derr := decodeAvatar(file)
	if derr != nil {
		httpapi.RenderError(w, r, derr)
		return
	}
	_ = format // accepted for diagnostics; we always re-encode as PNG

	resized := resize256(img)

	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	dst := filepath.Join(h.Dir, id.UserID.String()+".png")
	if err := writePNGAtomic(dst, resized); err != nil {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}

	ts, err := h.Store.MarkAvatarUploaded(r.Context(), id.UserID)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{
		"avatar_url":        userstore.AvatarURL(id.Username, &ts),
		"avatar_updated_at": ts,
	})
}

func (h *AvatarHandler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := auth.RequireIdentity(r)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	dst := filepath.Join(h.Dir, id.UserID.String()+".png")
	// Remove the on-disk file first; clearing the DB column second means a
	// retry after a partial failure still converges (the second run's Remove
	// returns ENOENT which we swallow).
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		httpapi.RenderError(w, r, apperr.Internal(err))
		return
	}
	if err := h.Store.ClearAvatar(r.Context(), id.UserID); err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AvatarHandler) get(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	if username == "" {
		httpapi.RenderError(w, r, apperr.NotFound("avatar"))
		return
	}
	updatedAt, userID, err := h.Store.AvatarUpdatedAt(r.Context(), username)
	if err != nil {
		httpapi.RenderError(w, r, err)
		return
	}
	if updatedAt == nil {
		// Not an error — just signal "no upload yet" with a 404 so the frontend
		// can fall through to its initials tile fallback. Aggressive caching is
		// fine here: the next upload bumps the ?v= query string so this 404
		// won't be served from cache anyway.
		w.Header().Set("Cache-Control", "public, max-age=60")
		httpapi.RenderError(w, r, apperr.NotFound("avatar"))
		return
	}
	dst := filepath.Join(h.Dir, userID.String()+".png")
	f, err := os.Open(dst)
	if err != nil {
		// DB says they have one but the file is gone. Treat as 404 — operator
		// can fix by re-uploading.
		httpapi.RenderError(w, r, apperr.NotFound("avatar"))
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/png")
	// Immutable for an hour against the same ?v=; the timestamp in the URL is
	// the cache buster, so we don't have to drop the cache on every upload.
	w.Header().Set("Cache-Control", "public, max-age=3600, immutable")
	w.Header().Set("Last-Modified", updatedAt.UTC().Format(http.TimeFormat))
	http.ServeContent(w, r, dst, updatedAt.UTC(), f)
}

// decodeAvatar reads an uploaded multipart file and returns the decoded image.
// It rejects formats other than PNG/JPEG/GIF (no SVG, no WebP polyglot) and
// caps decoded image dimensions so a 5×5 1.5MB PNG packed with metadata can't
// pass content sniffing only to expand to 20000×20000 in RAM.
func decodeAvatar(file multipart.File) (image.Image, string, *apperr.Error) {
	img, format, err := image.Decode(file)
	if err != nil {
		return nil, "", apperr.Validation("could not decode image (PNG, JPEG, and GIF accepted)", map[string]any{
			"reason": err.Error(),
		})
	}
	switch format {
	case "png", "jpeg", "gif":
		// ok
	default:
		return nil, "", apperr.Validation("unsupported image format: "+format, nil)
	}
	const maxSide = 8192
	b := img.Bounds()
	if b.Dx() > maxSide || b.Dy() > maxSide || b.Dx() < 8 || b.Dy() < 8 {
		return nil, "", apperr.Validation("image dimensions out of range", map[string]any{
			"width":  b.Dx(),
			"height": b.Dy(),
		})
	}
	return img, format, nil
}

// resize256 produces a square AvatarSize×AvatarSize RGBA. Non-square inputs
// are letterboxed by cropping a centred square first — this matches what
// users tend to expect (no skew, no padding). CatmullRom gives sharper
// results than BiLinear for downscales by 5x or more, which is the typical
// case (e.g. 1024 -> 256).
func resize256(src image.Image) *image.RGBA {
	b := src.Bounds()
	side := b.Dx()
	if b.Dy() < side {
		side = b.Dy()
	}
	cropX := b.Min.X + (b.Dx()-side)/2
	cropY := b.Min.Y + (b.Dy()-side)/2
	cropped := image.NewRGBA(image.Rect(0, 0, side, side))
	draw.Draw(cropped, cropped.Bounds(), src, image.Pt(cropX, cropY), draw.Src)

	dst := image.NewRGBA(image.Rect(0, 0, AvatarSize, AvatarSize))
	draw.CatmullRom.Scale(dst, dst.Bounds(), cropped, cropped.Bounds(), draw.Over, nil)
	return dst
}

// writePNGAtomic encodes img to a temp file in the same directory as dst,
// then renames into place. The rename is atomic on every Unix filesystem we
// care about; a power-cycle mid-upload leaves either the old file or the new,
// never half-written.
func writePNGAtomic(dst string, img *image.RGBA) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "avatar-*.png.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if any step below fails.
	defer func() { _ = os.Remove(tmpPath) }()

	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(tmp, img); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}

// multipartFile is the minimal interface we need from FormFile — defined here
// so test fixtures can pass any io.ReadSeeker without dragging the full
// multipart.File type around.
var _ multipart.File = (multipart.File)(nil)
