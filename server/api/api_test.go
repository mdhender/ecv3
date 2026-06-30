// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mdhender/ecv3/server/api"
	"github.com/mdhender/ecv3/server/auth"
	"github.com/mdhender/ecv3/server/store"
)

const testCookie = "ecv3_test_session"

// newServer builds the full handler chain (CrossOriginProtection -> session
// middleware -> API routes) over a fresh in-memory store, mirroring cmd/ec.
func newServer(t *testing.T) (*store.Store, *auth.Manager, http.Handler) {
	t.Helper()
	st, err := store.Create(store.MemoryPath)
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sm := auth.New(st, auth.Config{CookieName: testCookie, Secure: false, TTL: time.Hour})
	mux := http.NewServeMux()
	api.New(st, sm).Register(mux)
	handler := http.NewCrossOriginProtection().Handler(sm.Middleware(mux))
	return st, sm, handler
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func postLogin(h http.Handler, email, password string) *httptest.ResponseRecorder {
	body := `{"email":` + strconv.Quote(email) + `,"password":` + strconv.Quote(password) + `}`
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return do(h, r)
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == testCookie {
			return c
		}
	}
	t.Fatal("no session cookie in response")
	return nil
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	body, _ := io.ReadAll(w.Result().Body)
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return m
}

func TestLoginSuccess(t *testing.T) {
	st, _, h := newServer(t)
	id, err := st.CreateAccount(context.Background(), "alice@example.com", "s3cr3t", false)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	w := postLogin(h, "alice@example.com", "s3cr3t")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body %s)", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	c := sessionCookie(t, w)
	if c.Value == "" || !c.HttpOnly {
		t.Errorf("session cookie = %+v; want non-empty HttpOnly", c)
	}

	user := decode(t, w)["user"].(map[string]any)
	if user["email"] != "alice@example.com" || user["isAdmin"] != false {
		t.Errorf("user = %v; want alice@example.com non-admin", user)
	}
	if int64(user["id"].(float64)) != id {
		t.Errorf("user.id = %v; want %d", user["id"], id)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	st, _, h := newServer(t)
	if _, err := st.CreateAccount(context.Background(), "alice@example.com", "s3cr3t", false); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	cases := []struct{ name, email, password string }{
		{"wrong password", "alice@example.com", "nope"},
		{"unknown email", "ghost@example.com", "whatever"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := postLogin(h, tc.email, tc.password)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d; want 401", w.Code)
			}
			if decode(t, w)["error"] != "invalid email or password" {
				t.Errorf("error body = %v; want uniform message", decode(t, w))
			}
			// No session cookie should be issued.
			for _, c := range w.Result().Cookies() {
				if c.Name == testCookie && c.Value != "" {
					t.Error("a session cookie was set on a failed login")
				}
			}
		})
	}
}

func TestLoginMalformedBody(t *testing.T) {
	_, _, h := newServer(t)

	t.Run("invalid json", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("{not json"))
		if w := do(h, r); w.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", w.Code)
		}
	})
	t.Run("missing fields", func(t *testing.T) {
		if w := postLogin(h, "", ""); w.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", w.Code)
		}
	})
}

func TestLogout(t *testing.T) {
	st, _, h := newServer(t)
	if _, err := st.CreateAccount(context.Background(), "alice@example.com", "pw", false); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	cookie := sessionCookie(t, postLogin(h, "alice@example.com", "pw"))

	r := httptest.NewRequest(http.MethodDelete, "/api/session", nil)
	r.AddCookie(cookie)
	w := do(h, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204", w.Code)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == testCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout did not clear the cookie")
	}

	// The session is gone: /api/me with the old cookie is unauthenticated.
	r2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r2.AddCookie(cookie)
	if w2 := do(h, r2); w2.Code != http.StatusUnauthorized {
		t.Errorf("post-logout /api/me status = %d; want 401", w2.Code)
	}
}

func TestMeUnauthenticated(t *testing.T) {
	_, _, h := newServer(t)
	w := do(h, httptest.NewRequest(http.MethodGet, "/api/me", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.api+json" {
		t.Errorf("Content-Type = %q; want application/vnd.api+json", ct)
	}
	if _, ok := decode(t, w)["errors"]; !ok {
		t.Error("expected a JSON:API errors array")
	}
}

func TestMeAuthenticated(t *testing.T) {
	st, _, h := newServer(t)
	id, err := st.CreateAccount(context.Background(), "alice@example.com", "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	cookie := sessionCookie(t, postLogin(h, "alice@example.com", "pw"))

	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(cookie)
	w := do(h, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (body %s)", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.api+json" {
		t.Errorf("Content-Type = %q; want application/vnd.api+json", ct)
	}

	m := decode(t, w)
	data := m["data"].(map[string]any)
	if data["type"] != "users" || data["id"] != strconv.FormatInt(id, 10) {
		t.Errorf("data = %v; want type=users id=%d", data, id)
	}
	attrs := data["attributes"].(map[string]any)
	if attrs["email"] != "alice@example.com" || attrs["is-admin"] != false {
		t.Errorf("attributes = %v; want dasherized is-admin and email", attrs)
	}
	if m["meta"].(map[string]any)["impersonating"] != false {
		t.Errorf("meta.impersonating = %v; want false", m["meta"])
	}
}

func TestMeImpersonation(t *testing.T) {
	st, _, h := newServer(t)
	ctx := context.Background()
	adminID, err := st.CreateAccount(ctx, "root@example.com", "pw", true)
	if err != nil {
		t.Fatalf("CreateAccount admin: %v", err)
	}
	targetID, err := st.CreateAccount(ctx, "player@example.com", "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount target: %v", err)
	}
	cookie := sessionCookie(t, postLogin(h, "root@example.com", "pw"))

	// Mark the session as impersonating the target (id_hash = sha256 of token).
	sum := sha256.Sum256([]byte(cookie.Value))
	if err := st.SetImpersonation(ctx, hex.EncodeToString(sum[:]), targetID); err != nil {
		t.Fatalf("SetImpersonation: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(cookie)
	m := decode(t, do(h, r))

	data := m["data"].(map[string]any)
	if data["id"] != strconv.FormatInt(targetID, 10) {
		t.Errorf("data.id = %v; want effective (target) id %d", data["id"], targetID)
	}
	meta := m["meta"].(map[string]any)
	if meta["impersonating"] != true || meta["real-account-id"] != strconv.FormatInt(adminID, 10) {
		t.Errorf("meta = %v; want impersonating with real-account-id=%d", meta, adminID)
	}
}

func TestLoginCSRFRejected(t *testing.T) {
	st, _, h := newServer(t)
	if _, err := st.CreateAccount(context.Background(), "alice@example.com", "pw", false); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	body := `{"email":"alice@example.com","password":"pw"}`
	r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Sec-Fetch-Site", "cross-site") // browser cross-origin signal
	w := do(h, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403 (CrossOriginProtection)", w.Code)
	}
}
