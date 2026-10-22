package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const sessionCookie = "pulse_session"

type authState struct {
	Mode      string            `json:"mode"`
	Principal *domain.Principal `json:"principal"`
}

func (s *Server) authMe(c *gin.Context) error {
	p, ok := domain.CurrentPrincipal(c.Request.Context())
	var principal *domain.Principal
	if ok {
		principal = &p
	}
	mode := "legacy"
	if s.Access.Enabled() {
		mode = "local"
	}
	c.Header("Cache-Control", "no-store")
	return writeJSON(c, 200, authState{mode, principal})
}
func (s *Server) cookie(c *gin.Context, token string, age int) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: token, Path: s.Config.Prefix, MaxAge: age, HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteStrictMode})
}
func (s *Server) authLogin(c *gin.Context) error {
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	user, token, err := s.Access.Login(c.Request.Context(), in.Login, in.Password, c.RemoteIP())
	if err != nil {
		return err
	}
	s.cookie(c, token, 8*3600)
	c.Header("Cache-Control", "no-store")
	p := domain.AccountPrincipal(user)
	return writeJSON(c, 200, authState{"local", &p})
}
func (s *Server) authLogout(c *gin.Context) error {
	token, _ := c.Cookie(sessionCookie)
	if err := s.Access.Logout(c.Request.Context(), token); err != nil {
		return err
	}
	s.cookie(c, "", -1)
	c.Status(204)
	return nil
}
func (s *Server) authUsers(c *gin.Context) error {
	v, e := s.Access.Accounts(c.Request.Context())
	return respond(c, 200, v, e)
}
func (s *Server) authCreate(c *gin.Context) error {
	var in domain.AccountInput
	if e := readJSON(c, &in); e != nil {
		return e
	}
	v, e := s.Access.Create(c.Request.Context(), in)
	return respond(c, 201, v, e)
}
func (s *Server) authUpdate(c *gin.Context) error {
	n, e := id(c, "user_id")
	if e != nil {
		return e
	}
	var in domain.AccountInput
	if e = readJSON(c, &in); e != nil {
		return e
	}
	v, e := s.Access.Update(c.Request.Context(), n, in)
	return respond(c, 200, v, e)
}
func (s *Server) allowedOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		return c.GetHeader("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Host == c.Request.Host && (u.Scheme == "http" || u.Scheme == "https") {
		return true
	}
	for _, allowed := range s.Config.CORS {
		if origin == allowed {
			return true
		}
	}
	return false
}
func (s *Server) personalAuthorization(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	path := c.FullPath()
	if !strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/") {
		c.Next()
		return
	}
	deny := func(e error) { writeError(c, e); c.Abort() }
	if c.Request.Method != "GET" && !s.allowedOrigin(c) {
		deny(domain.NewError(domain.Forbidden, "Недопустимый источник запроса"))
		return
	}
	if path == s.Config.Prefix+"/auth/login" {
		c.Next()
		return
	}
	token, _ := c.Cookie(sessionCookie)
	p, err := s.Access.Identify(c.Request.Context(), token)
	if err != nil {
		if path == s.Config.Prefix+"/auth/me" && token == "" {
			c.Next()
			return
		}
		deny(err)
		return
	}
	c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), p))
	if path == s.Config.Prefix+"/auth/me" || path == s.Config.Prefix+"/auth/logout" {
		c.Next()
		return
	}
	if p.Role == "admin" {
		c.Next()
		return
	}
	relative := strings.TrimPrefix(path, s.Config.Prefix)
	allowed := false
	if c.Request.Method == "GET" {
		switch relative {
		case "/file-check-capabilities", "/wells/:well_id/file-check-jobs", "/wells/:well_id/file-check-jobs/:job_id", "/wells/:well_id/file-check-jobs/:job_id/result", "/wells/:well_id/file-check-jobs/:job_id/review":
			allowed = p.Role == "technologist"
		case "/ml/runs", "/ml/runs/:run_id/snapshot":
			allowed = s.ML != nil && s.ML.IsCohort()
		case "/events", "/events/:event_id", "/incidents/stats", "/wells", "/wells/export", "/ngdu", "/departments", "/parameters", "/ml/wells", "/ml/wells/:well_id/explain", "/ml/wells/:well_id/history", "/ml/alerts/:alert_id/review", "/presentation/summary":
			allowed = true
		case "/wells/:well_id/history", "/wells/:well_id", "/wells/:well_id/telemetry", "/wells/:well_id/telemetry/bounds", "/wells/:well_id/checks", "/wells/:well_id/checks/:check_id", "/wells/:well_id/checks/:check_id/review", "/wells/:well_id/risk", "/wells/:well_id/risk/history", "/wells/:well_id/predictions", "/wells/:well_id/predictions/export", "/wells/:well_id/complications", "/wells/:well_id/classification", "/wells/:well_id/incidents":
			allowed = true
		}
	} else if p.Role == "technologist" {
		switch relative {
		case "/wells/:well_id/file-check-jobs", "/wells/:well_id/file-check-jobs/:job_id/cancel":
			allowed = c.Request.Method == "POST"
		case "/wells/:well_id/file-check-jobs/:job_id/review":
			allowed = c.Request.Method == "PUT"
		case "/wells/:well_id/checks", "/wells/:well_id/telemetry/score", "/wells/:well_id/incidents":
			allowed = c.Request.Method == "POST"
		case "/wells/:well_id/checks/:check_id/review", "/ml/alerts/:alert_id/review":
			allowed = c.Request.Method == "PUT"
		}
	}
	if !allowed {
		deny(domain.NewError(domain.Forbidden, "Недостаточно прав для действия"))
		return
	}
	if strings.HasPrefix(relative, "/wells/:well_id") {
		n, e := id(c, "well_id")
		if e == nil {
			_, e = s.Catalog.Well(c.Request.Context(), n)
		}
		if e != nil {
			deny(e)
			return
		}
	}
	c.Next()
}
