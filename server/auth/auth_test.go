// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/mdhender/ecv3/server/store"
)

// testConfig uses a non-"__Host-" cookie name and Secure=false so the cookie
// round-trips over the plain-HTTP test transport.
func testConfig() Config {
	return Config{CookieName: "ecv3_test_session", Secure: false, TTL: time.Hour}
}

// newManager returns a Manager over a fresh in-memory store with a controllable
// clock, plus the account id to authenticate as.
func newManager(t *testing.T, cfg Config) (*Manager, *time.Time, int64) {
	t.Helper()
	st, err := store.Create(store.MemoryPath)
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	acct, err := st.CreateAccount(context.Background(), "a@example.com", "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	m := New(st, cfg)
	m.now = func() time.Time { return now }
	return m, &now, acct
}

// login runs Create and returns the issued cookie.
func login(t *testing.T, m *Manager, remoteAddr string, acct int64) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	if _, err := m.Create(r.Context(), w, r, acct); err != nil {
		t.Fatalf("Create: %v", err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("Create issued %d cookies; want 1 non-empty", len(cookies))
	}
	return cookies[0]
}

// resolved runs a request (carrying cookie, from remoteAddr) through Middleware
// and reports the Session the handler saw.
func resolved(m *Manager, cookie *http.Cookie, remoteAddr string) (Session, bool, *httptest.ResponseRecorder) {
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.RemoteAddr = remoteAddr
	if cookie != nil {
		r.AddCookie(cookie)
	}
	var got Session
	var ok bool
	w := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = FromContext(r.Context())
	})).ServeHTTP(w, r)
	return got, ok, w
}

func TestMiddlewareRoundTrip(t *testing.T) {
	m, _, acct := newManager(t, testConfig())
	cookie := login(t, m, "10.0.0.1:1111", acct)

	got, ok, _ := resolved(m, cookie, "10.0.0.1:2222")
	if !ok {
		t.Fatal("session not resolved")
	}
	if got.AccountID != acct || got.EffectiveAccountID != acct || got.Impersonating {
		t.Errorf("session = %+v; want account=%d, no impersonation", got, acct)
	}
}

func TestMiddlewareNoCookie(t *testing.T) {
	m, _, _ := newManager(t, testConfig())
	if _, ok, _ := resolved(m, nil, "10.0.0.1:1"); ok {
		t.Error("resolved a session with no cookie")
	}
}

func TestMiddlewareExpiredClearsCookie(t *testing.T) {
	m, now, acct := newManager(t, testConfig())
	cookie := login(t, m, "10.0.0.1:1", acct)

	*now = now.Add(2 * time.Hour) // past the 1h TTL

	_, ok, w := resolved(m, cookie, "10.0.0.1:1")
	if ok {
		t.Error("expired session resolved")
	}
	if !clears(w, m.cfg.CookieName) {
		t.Error("expired session did not clear the cookie")
	}
}

func TestMiddlewareIPBinding(t *testing.T) {
	t.Run("enforced rejects new IP", func(t *testing.T) {
		cfg := testConfig()
		cfg.BindIP = true
		m, _, acct := newManager(t, cfg)
		cookie := login(t, m, "10.0.0.1:1", acct)
		if _, ok, _ := resolved(m, cookie, "10.0.0.99:1"); ok {
			t.Error("IP-bound session resolved from a different IP")
		}
		// Same IP still works.
		if _, ok, _ := resolved(m, cookie, "10.0.0.1:5"); !ok {
			t.Error("IP-bound session rejected from its own IP")
		}
	})

	t.Run("disabled tolerates new IP", func(t *testing.T) {
		cfg := testConfig()
		cfg.BindIP = false
		m, _, acct := newManager(t, cfg)
		cookie := login(t, m, "10.0.0.1:1", acct)
		if _, ok, _ := resolved(m, cookie, "203.0.113.7:1"); !ok {
			t.Error("with binding off, a rotated IP was rejected")
		}
	})
}

func TestMiddlewareImpersonation(t *testing.T) {
	m, _, admin := newManager(t, testConfig())
	target, err := m.store.CreateAccount(context.Background(), "player@example.com", "pw", false)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	cookie := login(t, m, "10.0.0.1:1", admin)

	// Mark the session as impersonating the target.
	if err := m.store.SetImpersonation(context.Background(), hashToken(cookie.Value), target); err != nil {
		t.Fatalf("SetImpersonation: %v", err)
	}

	got, ok, _ := resolved(m, cookie, "10.0.0.1:1")
	if !ok {
		t.Fatal("session not resolved")
	}
	if got.AccountID != admin || got.EffectiveAccountID != target || !got.Impersonating {
		t.Errorf("session = %+v; want real=%d effective=%d impersonating", got, admin, target)
	}
}

func TestSlidingExpiry(t *testing.T) {
	m, now, acct := newManager(t, testConfig())
	cookie := login(t, m, "10.0.0.1:1", acct)

	// Advance 50 min (within the 1h TTL), resolve to slide the window forward.
	*now = now.Add(50 * time.Minute)
	if _, ok, _ := resolved(m, cookie, "10.0.0.1:1"); !ok {
		t.Fatal("session expired before its TTL")
	}
	// Another 50 min: would be past the ORIGINAL expiry, but the slide kept it alive.
	*now = now.Add(50 * time.Minute)
	if _, ok, _ := resolved(m, cookie, "10.0.0.1:1"); !ok {
		t.Error("sliding refresh did not extend the session")
	}
}

func TestDestroyLogsOut(t *testing.T) {
	m, _, acct := newManager(t, testConfig())
	cookie := login(t, m, "10.0.0.1:1", acct)

	r := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	if err := m.Destroy(r.Context(), w, r); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !clears(w, m.cfg.CookieName) {
		t.Error("Destroy did not clear the cookie")
	}
	if _, ok, _ := resolved(m, cookie, "10.0.0.1:1"); ok {
		t.Error("session still resolves after logout")
	}
}

func TestClientIP(t *testing.T) {
	// trust the loopback proxy and a sample internal proxy range.
	trusted := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("10.1.0.0/16"),
	}
	m := &Manager{cfg: Config{TrustedProxies: trusted}}

	cases := []struct {
		name       string
		remoteAddr string
		xff        []string // each value may itself be comma-separated
		want       string
	}{
		{
			name:       "untrusted peer, header ignored",
			remoteAddr: "203.0.113.5:9000",
			xff:        []string{"1.2.3.4"}, // spoof attempt
			want:       "203.0.113.5",
		},
		{
			name:       "trusted peer, single client",
			remoteAddr: "127.0.0.1:9000",
			xff:        []string{"198.51.100.7"},
			want:       "198.51.100.7",
		},
		{
			name:       "trusted peer, no header falls back to peer",
			remoteAddr: "127.0.0.1:9000",
			want:       "127.0.0.1",
		},
		{
			name:       "chain: skip trusted proxies from the right",
			remoteAddr: "127.0.0.1:9000",
			xff:        []string{"198.51.100.7, 10.1.2.3, 127.0.0.5"},
			want:       "198.51.100.7",
		},
		{
			name:       "spoofed left-hand entry is never reached",
			remoteAddr: "10.1.0.9:9000", // trusted proxy connects directly
			xff:        []string{"6.6.6.6", "198.51.100.7"},
			want:       "198.51.100.7",
		},
		{
			name:       "all entries trusted falls back to peer",
			remoteAddr: "127.0.0.1:9000",
			xff:        []string{"10.1.2.3, 127.0.0.9"},
			want:       "127.0.0.1",
		},
		{
			name:       "ipv4-mapped ipv6 peer matches ipv4 cidr",
			remoteAddr: "[::ffff:127.0.0.1]:9000",
			xff:        []string{"198.51.100.7"},
			want:       "198.51.100.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remoteAddr
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := m.clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestClientIPNoTrustedProxies confirms that with no trusted proxies the header
// is always ignored.
func TestClientIPNoTrustedProxies(t *testing.T) {
	m := &Manager{cfg: Config{}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:9000"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := m.clientIP(r); got != "127.0.0.1" {
		t.Errorf("clientIP = %q; want 127.0.0.1 (no proxy trusted)", got)
	}
}

// clears reports whether the response expires the named cookie.
func clears(w *httptest.ResponseRecorder, name string) bool {
	for _, c := range w.Result().Cookies() {
		if c.Name == name && c.MaxAge < 0 {
			return true
		}
	}
	return false
}
