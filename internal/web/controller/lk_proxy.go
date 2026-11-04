package controller

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

const lkCookie = "__Host-elx-lk"

// LK is a separate customer-facing identity plane. No administrator session
// or API bearer token is forwarded into this service, only the LK cookie.
func NewLKController(g *gin.RouterGroup) {
	g.GET("/lk", func(c *gin.Context) { serveDistPage(c, "lk.html") })
	g.GET("/lk/", func(c *gin.Context) { serveDistPage(c, "lk.html") })
	api := g.Group("/lk/api")
	bridge, err := newManagementBridge(strings.TrimSpace(os.Getenv("ELX_MANAGEMENT_SOCKET")))
	if err != nil {
		// Keep exactly the same endpoint surface even when Control is missing.
		bridge = &managementBridge{}
	}
	forward := func(c *gin.Context, method, path string, withBody bool) {
		c.Header("Cache-Control", "no-store")
		if bridge.client == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Control customer authentication is not configured"})
			return
		}
		var payload []byte
		if withBody {
			if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
				c.AbortWithStatus(http.StatusUnsupportedMediaType)
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
			body, readErr := io.ReadAll(c.Request.Body)
			if readErr != nil || len(body) > 4096 {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			payload = body
		}
		req, err := http.NewRequestWithContext(c.Request.Context(), method,
			"http://control.local/lk/v1"+path, bytes.NewReader(payload))
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if withBody {
			req.Header.Set("Content-Type", "application/json")
		}
		if token, err := c.Request.Cookie(lkCookie); err == nil {
			req.AddCookie(&http.Cookie{Name: lkCookie, Value: token.Value})
		}
		if csrf := c.GetHeader("X-ElX-CSRF"); csrf != "" {
			if len(csrf) > 128 {
				c.AbortWithStatus(http.StatusBadRequest)
				return
			}
			req.Header.Set("X-ElX-CSRF", csrf)
		}
		response, err := bridge.client.Do(req)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Control customer service unavailable"})
			return
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
		if err != nil || len(data) > 1<<20 {
			c.AbortWithStatus(http.StatusBadGateway)
			return
		}
		for _, cookie := range response.Cookies() {
			if cookie.Name == lkCookie && cookie.Secure && cookie.HttpOnly &&
				cookie.Path == "/" && cookie.SameSite == http.SameSiteStrictMode {
				c.Writer.Header().Add("Set-Cookie", cookie.String())
			}
		}
		c.Data(response.StatusCode, "application/json; charset=utf-8", data)
	}
	uuidRoute := func(c *gin.Context) bool {
		if !managementUUID(c.Param("id")) {
			c.AbortWithStatus(http.StatusBadRequest)
			return false
		}
		return true
	}
	api.POST("/register", func(c *gin.Context) { forward(c, http.MethodPost, "/register", true) })
	api.POST("/login", func(c *gin.Context) { forward(c, http.MethodPost, "/login", true) })
	api.POST("/logout", func(c *gin.Context) { forward(c, http.MethodPost, "/logout", false) })
	api.GET("/me", func(c *gin.Context) { forward(c, http.MethodGet, "/me", false) })
	api.POST("/claim", func(c *gin.Context) { forward(c, http.MethodPost, "/claim", true) })
	api.GET("/entitlements", func(c *gin.Context) { forward(c, http.MethodGet, "/entitlements", false) })
	api.GET("/entitlements/:id", func(c *gin.Context) {
		if uuidRoute(c) {
			forward(c, http.MethodGet, "/entitlements/"+c.Param("id"), false)
		}
	})
	api.POST("/entitlements/:id/sessions/:owner/drain", func(c *gin.Context) {
		if uuidRoute(c) && managementUUID(c.Param("owner")) {
			forward(c, http.MethodPost, "/entitlements/"+c.Param("id")+"/sessions/"+c.Param("owner")+"/drain", false)
		} else {
			c.AbortWithStatus(http.StatusBadRequest)
		}
	})
	api.POST("/entitlements/:id/devices/:device/revoke", func(c *gin.Context) {
		if uuidRoute(c) && managementUUID(c.Param("device")) {
			forward(c, http.MethodPost, "/entitlements/"+c.Param("id")+"/devices/"+c.Param("device")+"/revoke", false)
		} else {
			c.AbortWithStatus(http.StatusBadRequest)
		}
	})
	api.POST("/entitlements/:id/fup-schedules", func(c *gin.Context) {
		if uuidRoute(c) {
			forward(c, http.MethodPost, "/entitlements/"+c.Param("id")+"/fup-schedules", true)
		}
	})
}
