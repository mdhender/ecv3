// Copyright (c) 2026 Michael D Henderson. All rights reserved.

// Package api implements the ecv3 JSON HTTP API. The DTOs in this file are the
// single source of truth for the wire format — the `json` tags define the exact
// field names the Ember SPA sees. The human-readable contract lives in
// docs/api.md; the httptest suite in this package verifies that code and doc
// agree. Keep all three in step.
//
// Format split (see docs/api.md): auth *actions* (login, logout) use plain JSON;
// *resources* the WarpDrive cache consumes (currently /api/me) use JSON:API.
package api

// --- Plain-JSON auth actions ---

// loginRequest is the POST /api/login body.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the POST /api/login success body. The cookie carries the
// session; this is just the non-secret user summary for the SPA.
type loginResponse struct {
	User userObject `json:"user"`
}

// userObject is the plain-JSON user summary (camelCase, as JS expects).
type userObject struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"isAdmin"`
}

// errorResponse is the plain-JSON error envelope for the action endpoints.
type errorResponse struct {
	Error string `json:"error"`
}

// --- JSON:API resources ---

// meDocument is the GET /api/me success body (Content-Type
// application/vnd.api+json). Attribute keys are dasherized per JSON:API
// convention.
type meDocument struct {
	Data meResource `json:"data"`
	Meta meMeta     `json:"meta"`
}

type meResource struct {
	Type       string       `json:"type"` // always "users"
	ID         string       `json:"id"`   // stringified account id
	Attributes meAttributes `json:"attributes"`
}

type meAttributes struct {
	Email   string `json:"email"`
	IsAdmin bool   `json:"is-admin"`
}

// meMeta reports impersonation. When an admin is acting as another account,
// Data is the impersonated (effective) account and RealAccountID is the admin's
// own id; otherwise Impersonating is false and RealAccountID is omitted.
type meMeta struct {
	Impersonating bool   `json:"impersonating"`
	RealAccountID string `json:"real-account-id,omitempty"`
}

// jsonapiErrorDocument is the JSON:API error envelope (used for /api/me 401).
type jsonapiErrorDocument struct {
	Errors []jsonapiError `json:"errors"`
}

type jsonapiError struct {
	Status string `json:"status"`
	Title  string `json:"title"`
}
