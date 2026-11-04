package controller

import (
	"context"
	"errors"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"

	"github.com/gin-gonic/gin"
)

// This bridge exposes a fixed set of server-owned Control paths; the caller
// never supplies an arbitrary URL, a socket path, or Control credentials.
func managementAuthorized(c *gin.Context) bool {
	if scope, exists := c.Get("api_token_scope"); exists {
		return scope == model.ApiScopeAdmin
	}
	return session.IsLogin(c)
}

func managementUUID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}

type managementBridge struct {
	socket string
	client *http.Client
}

func newManagementBridge(socket string) (*managementBridge, error) {
	if !filepath.IsAbs(socket) || socket == "/" {
		return nil, errors.New("invalid Control socket")
	}
	return &managementBridge{
		socket: socket,
		client: &http.Client{
			Timeout: 8 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 2*time.Second}).DialContext(ctx, "unix", socket)
				},
			},
		},
	}, nil
}

func (b *managementBridge) forward(c *gin.Context, method, path string) {
	b.forwardPayload(c,method,path,nil)
}

func (b *managementBridge) forwardPayload(c *gin.Context, method, path string, payload []byte) {
	c.Header("Cache-Control","no-store")
	if !managementAuthorized(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "msg": "administrator authentication required"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, "http://control.local"+path, bytes.NewReader(payload))
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if len(payload)>0 { req.Header.Set("Content-Type","application/json") }
	resp, err := b.client.Do(req)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "msg": "ElX Control is unavailable"})
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"success": false, "msg": "invalid ElX Control response"})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not copy backend error payloads, connection hints, or internal
		// database state into an HTTP response exposed by the panel.
		c.AbortWithStatusJSON(resp.StatusCode, gin.H{"success": false, "msg": http.StatusText(resp.StatusCode)})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(resp.StatusCode, "application/json; charset=utf-8", append(append([]byte(`{"success":true,"obj":`), data...), '}'))
}

func registerManagementRoutes(api *gin.RouterGroup) {
	g := api.Group("/elx")
	// All routes are explicitly registered even when Control is not running:
	// callers receive 503, never silently fall back to an unsafe SQL path.
	bridge, err := newManagementBridge(strings.TrimSpace(os.Getenv("ELX_MANAGEMENT_SOCKET")))
	if err != nil {
		g.Any("/*path", func(c *gin.Context) {
			if !managementAuthorized(c) {
				c.AbortWithStatus(http.StatusForbidden)
			} else {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "msg": "ElX Control is not configured"})
			}
		})
		return
	}
	g.GET("/entitlements", func(c *gin.Context) {
		if !managementAuthorized(c) { c.AbortWithStatus(http.StatusForbidden); return }
		limit := 50
		if raw := c.Query("limit"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > 100 { c.AbortWithStatus(http.StatusBadRequest); return }
			limit = v
		}
		after := c.Query("after")
		if after != "" && !managementUUID(after) { c.AbortWithStatus(http.StatusBadRequest); return }
		path := "/v1/entitlements?limit="+strconv.Itoa(limit)
		if after != "" { path += "&after="+after }
		bridge.forward(c, http.MethodGet, path)
	})
	g.GET("/entitlements/:id", func(c *gin.Context) {
		if !managementUUID(c.Param("id")) { c.AbortWithStatus(http.StatusBadRequest); return }
		bridge.forward(c,http.MethodGet,"/v1/entitlements/"+c.Param("id"))
	})
	g.POST("/entitlements/:id/sessions/:owner/drain", func(c *gin.Context) {
		if !managementUUID(c.Param("id")) || !managementUUID(c.Param("owner")) { c.AbortWithStatus(http.StatusBadRequest); return }
		bridge.forward(c,http.MethodPost,"/v1/entitlements/"+c.Param("id")+"/sessions/"+c.Param("owner")+"/drain")
	})
	g.POST("/entitlements/:id/devices/:device/revoke", func(c *gin.Context) {
		if !managementUUID(c.Param("id")) || !managementUUID(c.Param("device")) {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		bridge.forward(c,http.MethodPost,"/v1/entitlements/"+c.Param("id")+"/devices/"+c.Param("device")+"/revoke")
	})
	g.POST("/entitlements/:id/fup-schedules", func(c *gin.Context) {
		id:=c.Param("id")
		if !managementUUID(id){c.AbortWithStatus(http.StatusBadRequest);return}
		var spec struct {
			ScheduleID string `json:"scheduleId"`
			Cadence string `json:"cadence"`
			Timezone string `json:"timezone"`
			Direction string `json:"direction"`
			ThresholdBytes int64 `json:"thresholdBytes"`
			ReducedUp int64 `json:"reducedUp"`
			ReducedDown int64 `json:"reducedDown"`
		}
		c.Request.Body=http.MaxBytesReader(c.Writer,c.Request.Body,4096)
		if c.ShouldBindJSON(&spec)!=nil || !managementUUID(spec.ScheduleID){
			c.AbortWithStatus(http.StatusBadRequest);return
		}
		payload,err:=json.Marshal(spec)
		if err!=nil {c.AbortWithStatus(http.StatusBadRequest);return}
		bridge.forwardPayload(c,http.MethodPost,"/v1/entitlements/"+id+"/fup-schedules",payload)
	})
	g.POST("/entitlements/:id/fup-schedules/:schedule/activate",func(c *gin.Context){
		entitlement,schedule:=c.Param("id"),c.Param("schedule")
		if !managementUUID(entitlement)||!managementUUID(schedule){
			c.AbortWithStatus(http.StatusBadRequest);return
		}
		bridge.forward(c,http.MethodPost,
			"/v1/entitlements/"+entitlement+"/fup-schedules/"+schedule+"/activate")
	})
	g.POST("/entitlements/:id/reconcile-fup", func(c *gin.Context) {
		if !managementUUID(c.Param("id")) { c.AbortWithStatus(http.StatusBadRequest); return }
		bridge.forward(c,http.MethodPost,"/v1/entitlements/"+c.Param("id")+"/reconcile-fup")
	})
}
