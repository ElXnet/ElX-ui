package controller

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestManagementFUPScheduleProxyAndStrictIDs(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	type seen struct{ method, path, body string }
	received := make(chan seen, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(io.LimitReader(r.Body, 4097))
		received <- seen{r.Method, r.URL.Path, string(data)}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"scheduleId": "00000000-0000-4000-8000-000000000041"})
	})}
	go server.Serve(listener)
	defer server.Shutdown(context.Background())
	t.Setenv("ELX_MANAGEMENT_SOCKET", socket)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeAdmin); c.Next() })
	api := engine.Group("/panel/api")
	registerManagementRoutes(api)

	path := "/panel/api/elx/entitlements/" + mgmtEntitlement + "/fup-schedules"
	body := `{"scheduleId":"00000000-0000-4000-8000-000000000041","cadence":"month","timezone":"UTC","direction":"total","thresholdBytes":1000000,"reducedUp":125000,"reducedDown":125000}`
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("proxy status %d: %s", w.Code, w.Body.String())
	}
	x := <-received
	if x.method != "POST" || x.path != "/v1/entitlements/"+mgmtEntitlement+"/fup-schedules" {
		t.Fatalf("unexpected trusted path %+v", x)
	}
	if x.body != body {
		t.Fatalf("FUP content changed: %s", x.body)
	}
	req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"scheduleId":"bad"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("bad schedule ID accepted: %d", w.Code)
	}
}

func TestManagementRejectsNodeScopeFUPMutations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("api_token_scope", model.ApiScopeNodeSync); c.Next() })
	api := engine.Group("/panel/api")
	registerManagementRoutes(api)
	req := httptest.NewRequest(http.MethodPost,
		"/panel/api/elx/entitlements/"+mgmtEntitlement+"/fup-schedules",
		strings.NewReader(`{"scheduleId":"00000000-0000-4000-8000-000000000041"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("node-sync could configure FUP: %d", w.Code)
	}
}
