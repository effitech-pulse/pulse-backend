package httpapi

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (s *Server) auditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if !strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/") {
			return
		}
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			s.recordAudit(c, c.Writer.Status())
		}
	}
}

func (s *Server) recoverPanic(c *gin.Context, recovered any) {
	slog.Error("handler panic", "error", recovered)
	if !c.Writer.Written() {
		writeError(c, fmt.Errorf("handler panic"))
	}
	c.Abort()
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		for _, allowed := range s.Config.CORS {
			if origin == allowed {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Credentials", "true")
				break
			}
		}
		if c.Request.Method == http.MethodOptions && origin != "" {
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type,X-API-Key")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) authorizeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Access.Enabled() {
			s.personalAuthorization(c)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/") && s.Config.APIKey != "" &&
			subtle.ConstantTimeCompare([]byte(c.GetHeader("X-API-Key")), []byte(s.Config.APIKey)) != 1 {
			writeError(c, domain.NewError(domain.Unauthorized, "Invalid or missing X-API-Key"))
			c.Abort()
			return
		}
		p := domain.Principal{Role: "admin", Name: "Локальный сеанс", Mechanism: "legacy", Scope: domain.AccessScope{Funds: []string{"operational", "presentation"}}}
		if s.Config.APIKey != "" {
			p.Name = "Общий API-ключ"
		}
		c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}
