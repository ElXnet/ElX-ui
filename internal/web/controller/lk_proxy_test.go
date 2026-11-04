package controller

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLKProxyForwardsOnlyLKCookieAndCSRF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lk.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type observation struct {
		path, cookie, csrf, body string
	}
	seen := make(chan observation, 2)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen <- observation{r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("X-ElX-CSRF"), string(body)}
		w.Header().Set("Content-Type", "application/json")
		http.SetCookie(w, &http.Cookie{
			Name: lkCookie, Value: "new-session-token", Secure: true,
			HttpOnly: true, Path: "/", SameSite: http.SameSiteStrictMode,
		})
		_, _ = w.Write([]byte(`{"id":"example","email":"redacted","csrf":"csrf-token"}`))
	})}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	t.Setenv("ELX_MANAGEMENT_SOCKET", path)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewLKController(engine.Group("/"))

	req := httptest.NewRequest(http.MethodGet, "/lk/api/me", nil)
	req.Header.Set("Cookie", "3x-ui=administrator; "+lkCookie+"=user-secret")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "redacted") {
		t.Fatalf("LK response %d %s", w.Code, w.Body.String())
	}
	o := <-seen
	if o.path != "/lk/v1/me" || strings.Contains(o.cookie, "administrator") || o.cookie != lkCookie+"=user-secret" {
		t.Fatalf("cookie or path escalation: %+v", o)
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), "SameSite=Strict") {
		t.Fatalf("secure LK cookie not forwarded: %q", w.Header().Get("Set-Cookie"))
	}

	req = httptest.NewRequest(http.MethodPost, "/lk/api/claim", strings.NewReader(`{"invite":"code"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ElX-CSRF", "my-csrf")
	req.Header.Set("Cookie", lkCookie+"=user-secret; Authorization=administrator")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("claim request failed %d", w.Code)
	}
	o = <-seen
	if o.path != "/lk/v1/claim" || o.csrf != "my-csrf" || o.body != `{"invite":"code"}` ||
		strings.Contains(o.cookie, "administrator") {
		t.Fatalf("claim escalation: %+v", o)
	}
}

func TestLKProxyRejectsInvalidDeviceAndUnknownEndpoint(t *testing.T) {
	t.Setenv("ELX_MANAGEMENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewLKController(engine.Group("/"))

	req := httptest.NewRequest(http.MethodPost, "/lk/api/entitlements/not-uuid/devices/not-uuid/revoke", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("untrusted IDs forwarded: %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/lk/api/admin", nil)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unexpected public LK route: %d", w.Code)
	}

	// Customer cookies must never get a path to enable FUP or bypass signed
	// usage coverage, even if they own a subscription.
	req = httptest.NewRequest(
		http.MethodPost,
		"/lk/api/entitlements/00000000-0000-4000-8000-000000000001/fup-schedules/00000000-0000-4000-8000-000000000002/activate",
		nil,
	)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("LK can activate FUP without trusted operator: %d", w.Code)
	}
}
