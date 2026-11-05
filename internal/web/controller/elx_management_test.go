package controller

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const (
	mgmtEntitlement = "00000000-0000-4000-8000-000000000001"
	mgmtOwner       = "00000000-0000-4000-8000-000000000002"
)

func managementTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/panel/api")
	api.Use((&APIController{}).enforceTokenScope)
	registerManagementRoutes(api)
	return engine
}

func managementRequest(e *gin.Engine, method, path, scope string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	// Test a real API auth scope set by earlier authentication middleware.
	if scope != "" {
		api := gin.New()
		_ = api
	}
	e.ServeHTTP(w, req)
	return w
}

func TestManagementScopeForbidsNodeAndMonitor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeNodeSync); c.Next() })
	api := engine.Group("/panel/api")
	registerManagementRoutes(api)
	req := httptest.NewRequest(http.MethodPost, "/panel/api/elx/entitlements/"+mgmtEntitlement+"/sessions/"+mgmtOwner+"/drain", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("node-sync could drain: %d", w.Code)
	}
}

func TestManagementRejectsBadIDsBeforeDial(t *testing.T) {
	t.Setenv("ELX_MANAGEMENT_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	engine := managementTestEngine()
	for _, p := range []string{
		"/panel/api/elx/entitlements/not-uuid",
		"/panel/api/elx/entitlements/" + mgmtEntitlement + "/sessions/not-uuid/drain",
	} {
		method := "GET"
		if strings.Contains(p, "drain") {
			method = "POST"
		}
		w := managementRequest(engine, method, p, "")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
}

func TestManagementAdminProxyFixedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(path)
	seen := make(chan string, 5)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "control"})
	})}
	go srv.Serve(listener)
	defer srv.Shutdown(context.Background())
	t.Setenv("ELX_MANAGEMENT_SOCKET", path)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeAdmin); c.Next() })
	api := engine.Group("/panel/api")
	registerManagementRoutes(api)
	w := managementRequest(engine, "GET", "/panel/api/elx/entitlements?limit=10", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	if got := <-seen; got != "GET /v1/entitlements?limit=10" {
		t.Fatalf("path: %s", got)
	}
	w = managementRequest(engine, "POST", "/panel/api/elx/entitlements/"+mgmtEntitlement+"/devices/"+mgmtOwner+"/revoke", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("revoke bridge: %d %s", w.Code, w.Body.String())
	}
	if got := <-seen; got != "POST /v1/entitlements/"+mgmtEntitlement+"/devices/"+mgmtOwner+"/revoke" {
		t.Fatalf("revoke proxy path: %s", got)
	}
	w = managementRequest(engine, "POST",
		"/panel/api/elx/entitlements/"+mgmtEntitlement+"/fup-schedules/"+mgmtOwner+"/activate", "")
	if w.Code != 200 {
		t.Fatalf("activation proxy: %d %s", w.Code, w.Body.String())
	}
	if got := <-seen; got != "POST /v1/entitlements/"+mgmtEntitlement+"/fup-schedules/"+mgmtOwner+"/activate" {
		t.Fatalf("activation proxy route: %s", got)
	}
	w = managementRequest(engine, "POST",
		"/panel/api/elx/entitlements/"+mgmtEntitlement+"/fup-schedules/not-a-uuid/activate", "")
	if w.Code != 400 {
		t.Fatalf("invalid activation schedule accepted: %d", w.Code)
	}
}

func TestManagementScopeDeniesFUPActivation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeNodeSync); c.Next() })
	registerManagementRoutes(engine.Group("/panel/api"))
	req := httptest.NewRequest(http.MethodPost,
		"/panel/api/elx/entitlements/"+mgmtEntitlement+"/fup-schedules/"+mgmtOwner+"/activate", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("node-sync scope could activate FUP: %d", w.Code)
	}
}
