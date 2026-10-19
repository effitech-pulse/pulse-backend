package httpapi

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/gin-gonic/gin"
)

func (s *Server) integrationSources(c *gin.Context) error {
	v, err := s.Integrations.List(c.Request.Context())
	return respond(c, 200, v, err)
}
func (s *Server) integrationSave(c *gin.Context) error {
	var in domain.SourceInput
	if err := readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Integrations.Save(c.Request.Context(), c.Param("source_id"), in)
	return respond(c, 200, v, err)
}
func (s *Server) integrationStatus(c *gin.Context) error {
	var in struct {
		Status string `json:"status"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Integrations.Status(c.Request.Context(), c.Param("source_id"), in.Status)
	return respond(c, 200, v, err)
}
func (s *Server) integrationCheck(c *gin.Context) error {
	var in domain.SourceInput
	if err := readJSON(c, &in); err != nil {
		return err
	}
	records, err := s.Integrations.Check(c.Request.Context(), in)
	if err != nil {
		return domain.NewError(domain.Unavailable, "Проверка не пройдена. Проверьте адрес, доступ, таблицу и INTEGRATION_ALLOWED_HOSTS.")
	}
	message := "Подключение проверено"
	if in.Kind == "json_push" {
		message = "Адрес приёма будет доступен после сохранения и запуска"
	}
	return writeJSON(c, 200, struct {
		Records []map[string]any `json:"records"`
		Message string           `json:"message"`
	}{records, message})
}
func (s *Server) integrationCollect(c *gin.Context) error {
	v, err := s.Integrations.Collect(c.Request.Context(), c.Param("source_id"), false)
	return respond(c, 200, v, err)
}
func (s *Server) integrationLogs(c *gin.Context) error {
	v, err := s.Integrations.Logs(c.Request.Context())
	return respond(c, 200, v, err)
}
func (s *Server) integrationIngest(c *gin.Context) error {
	var in struct {
		Records json.RawMessage `json:"records"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	var records []map[string]any
	dec := json.NewDecoder(bytes.NewReader(in.Records))
	dec.UseNumber()
	if err := dec.Decode(&records); err != nil {
		return validation("Нужен массив объектов records")
	}
	if len(records) == 0 || len(records) > 10000 {
		return validation("Нужен массив records от 1 до 10000 записей")
	}
	v, err := s.Integrations.Ingest(c.Request.Context(), c.Param("source_id"), records)
	return respond(c, 200, v, err)
}
func (s *Server) telemetryRead(c *gin.Context) error {
	start, err := time.Parse(time.RFC3339Nano, c.Query("start"))
	if err != nil {
		return validation("Некорректное начало периода")
	}
	end, err := time.Parse(time.RFC3339Nano, c.Query("end"))
	if err != nil {
		return validation("Некорректный конец периода")
	}
	v, err := s.Integrations.Read(c.Request.Context(), c.Query("well_id"), start, end)
	return respond(c, 200, v, err)
}
func (s *Server) telemetryImport(c *gin.Context) error {
	var in struct {
		Points []domain.Measurement `json:"points"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Integrations.Import(c.Request.Context(), in.Points)
	return respond(c, 200, v, err)
}

func (s *Server) telemetryScore(c *gin.Context) error {
	var in struct {
		WellID string    `json:"well_id"`
		Start  time.Time `json:"start"`
		End    time.Time `json:"end"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Integrations.Score(c.Request.Context(), in.WellID, in.Start, in.End)
	return respond(c, 200, v, err)
}

func (s *Server) telemetryBounds(c *gin.Context) error {
	v, err := s.Integrations.Bounds(c.Request.Context(), c.Query("well_id"))
	return respond(c, 200, v, err)
}

func (s *Server) appTelemetryWell(c *gin.Context) (domain.Well, error) {
	n, err := id(c, "well_id")
	if err != nil {
		return domain.Well{}, err
	}
	return s.Catalog.Well(c.Request.Context(), n)
}
func (s *Server) appTelemetryRead(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	start, err := time.Parse(time.RFC3339Nano, c.Query("start"))
	if err != nil {
		return validation("Некорректное начало периода")
	}
	end, err := time.Parse(time.RFC3339Nano, c.Query("end"))
	if err != nil {
		return validation("Некорректный конец периода")
	}
	v, err := s.Integrations.ReadApp(c.Request.Context(), well, start, end)
	return respond(c, 200, v, err)
}
func (s *Server) appTelemetryBounds(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	v, err := s.Integrations.BoundsApp(c.Request.Context(), well)
	return respond(c, 200, v, err)
}
func (s *Server) appTelemetryScore(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	var in struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Integrations.ScoreApp(c.Request.Context(), well, in.Start, in.End)
	return respond(c, 200, v, err)
}
func (s *Server) appTelemetryImport(c *gin.Context) error {
	var in struct {
		Points []domain.Measurement `json:"points"`
	}
	if err := readJSON(c, &in); err != nil {
		return err
	}
	points, err := s.Catalog.PulseTelemetry(c.Request.Context(), in.Points)
	if err != nil {
		return err
	}
	v, err := s.Integrations.Import(c.Request.Context(), points)
	return respond(c, 200, v, err)
}
