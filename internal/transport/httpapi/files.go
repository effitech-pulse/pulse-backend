package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
)

func uploadFile(c *gin.Context) (func(), error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024*1024)
	form, err := c.MultipartForm()
	if err != nil {
		return nil, validation("Некорректный файл")
	}
	return func() { _ = form.RemoveAll() }, nil
}

func attachment(c *gin.Context, filename string, data []byte, err error) error {
	if err != nil {
		return err
	}
	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, err = c.Writer.Write(data)
	return err
}

func (s *Server) importWells(c *gin.Context) error {
	clean, err := uploadFile(c)
	if err != nil {
		return err
	}
	defer clean()
	header, err := c.FormFile("file")
	if err != nil {
		return validation("Не передан файл")
	}
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	name := strings.ToLower(header.Filename)
	if !strings.HasSuffix(name, ".xlsx") && !strings.HasSuffix(name, ".xlsm") {
		return validation("Ожидается файл Excel (.xlsx)")
	}
	rows, failures, err := spreadsheet.ReadWells(file)
	if err != nil {
		return err
	}
	result, err := s.Catalog.ImportWells(c.Request.Context(), rows)
	if err != nil {
		return err
	}
	result.Errors = failures
	return writeJSON(c, 200, result)
}

func (s *Server) exportWells(c *gin.Context) error {
	active, err := queryBool(c, "active_only", false)
	if err != nil {
		return err
	}
	filter, err := wellFilter(c)
	if err != nil {
		return err
	}
	rows, err := s.Catalog.FilteredWells(c.Request.Context(), active, filter)
	if err != nil {
		return err
	}
	var data []byte
	if c.Query("identity") == "pulse" {
		extended, e := queryBool(c, "lifecycle", false)
		if e != nil {
			return e
		}
		if extended {
			data, err = spreadsheet.WriteLifecycleWells(rows)
		} else {
			data, err = spreadsheet.WritePulseWells(rows)
		}
	} else {
		data, err = spreadsheet.WriteWells(rows)
	}
	return attachment(c, "wells.xlsx", data, err)
}

func (s *Server) exportPredictions(c *gin.Context) error {
	id, err := id(c, "well_id")
	if err != nil {
		return err
	}
	rows, err := s.Catalog.Predictions(c.Request.Context(), id, 5000)
	if err != nil {
		return err
	}
	data, err := spreadsheet.WritePredictions(rows)
	return attachment(c, fmt.Sprintf("well_%d_predictions.xlsx", id), data, err)
}

func (s *Server) exportRuns(c *gin.Context) error {
	filter, err := runFilters(c, 5000)
	if err != nil {
		return err
	}
	rows, err := s.Catalog.Runs(c.Request.Context(), filter)
	if err != nil {
		return err
	}
	data, err := spreadsheet.WriteRuns(rows)
	return attachment(c, "runs.xlsx", data, err)
}
