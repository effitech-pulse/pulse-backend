package httpapi

import "github.com/gin-gonic/gin"

func (s *Server) presentationSummary(c *gin.Context) error {
	out, err := s.Presentation.Summary(c.Request.Context())
	return respond(c, 200, out, err)
}

func (s *Server) presentationScenario(c *gin.Context) error {
	out, err := s.Presentation.Scenario(c.Request.Context())
	return respond(c, 200, out, err)
}
