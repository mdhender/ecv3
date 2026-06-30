// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/mdhender/ecv3/server/auth"
	"github.com/mdhender/ecv3/server/store"
)

// maxLoginBody caps the login request body. Credentials are tiny; anything
// larger is a mistake or abuse.
const maxLoginBody = 4 << 10 // 4 KiB

const contentTypeJSON = "application/json"
const contentTypeJSONAPI = "application/vnd.api+json"

// API holds the dependencies the handlers need.
type API struct {
	store    *store.Store
	sessions *auth.Manager
}

// New builds the API handler set.
func New(st *store.Store, sm *auth.Manager) *API {
	return &API{store: st, sessions: sm}
}

// Register mounts the API routes onto mux using Go 1.22+ method+pattern routes.
// The session middleware (wired in cmd/ec) must wrap mux so /api/me can read the
// resolved identity from the request context.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/healthz", a.healthz)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("DELETE /api/session", a.logout)
	mux.HandleFunc("GET /api/me", a.me)
}

// healthz reports data-layer reachability.
func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", contentTypeJSON)
	if err := a.store.Ping(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
		return
	}
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// login verifies credentials, starts a session (setting the cookie), and returns
// the user summary. Failures use a single uniform 401 so the response never
// reveals whether an email exists.
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	acct, err := a.store.AuthenticateByPassword(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if _, err := a.sessions.Create(r.Context(), w, r, acct.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, contentTypeJSON, loginResponse{
		User: userObject{ID: acct.ID, Email: acct.Email, IsAdmin: acct.IsAdmin},
	})
}

// logout destroys the request's session and clears the cookie. It is idempotent:
// a request without a session still returns 204.
func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.sessions.Destroy(r.Context(), w, r); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// me returns the authenticated user as a JSON:API resource, reporting the
// effective (possibly impersonated) identity. Unauthenticated requests get a
// JSON:API 401 so ember-simple-auth's restore() rejects cleanly.
func (a *API) me(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.FromContext(r.Context())
	if !ok {
		writeJSONAPIError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	acct, found, err := a.store.GetAccount(r.Context(), sess.EffectiveAccountID)
	if err != nil {
		writeJSONAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !found {
		// The session points at an account that no longer exists.
		writeJSONAPIError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	doc := meDocument{
		Data: meResource{
			Type: "users",
			ID:   strconv.FormatInt(acct.ID, 10),
			Attributes: meAttributes{
				Email:   acct.Email,
				IsAdmin: acct.IsAdmin,
			},
		},
		Meta: meMeta{Impersonating: sess.Impersonating},
	}
	if sess.Impersonating {
		doc.Meta.RealAccountID = strconv.FormatInt(sess.AccountID, 10)
	}
	writeJSON(w, http.StatusOK, contentTypeJSONAPI, doc)
}

// writeJSON encodes v as JSON with the given status and content type.
func writeJSON(w http.ResponseWriter, status int, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a plain-JSON {"error": msg} body (the action-endpoint
// error envelope).
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, contentTypeJSON, errorResponse{Error: msg})
}

// writeJSONAPIError writes a JSON:API error document.
func writeJSONAPIError(w http.ResponseWriter, status int, title string) {
	writeJSON(w, status, contentTypeJSONAPI, jsonapiErrorDocument{
		Errors: []jsonapiError{{Status: strconv.Itoa(status), Title: title}},
	})
}
