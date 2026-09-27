package server

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/prismgroup/query-api/internal/query"
	"github.com/prismgroup/query-api/internal/schema"
)

type modelInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TimeField   string `json:"timeField,omitempty"`
	Index       string `json:"index"`
	DocCount    int64  `json:"docCount"`
}

func (s *Server) models(c echo.Context) error {
	ctx := c.Request().Context()
	counts := map[string]int64{}
	var cat []struct {
		Index string `json:"index"`
		Count string `json:"docs.count"`
	}
	if err := s.os.Do(ctx, http.MethodGet, "/_cat/indices/"+s.registry.Prefix()+"*?format=json&h=index,docs.count", nil, &cat); err == nil {
		for _, row := range cat {
			counts[row.Index] = atoi(row.Count)
		}
	}
	out := []modelInfo{}
	for _, m := range s.registry.Public() {
		idx := s.registry.IndexName(m.Name)
		out = append(out, modelInfo{Name: m.Name, Title: m.Title, Description: m.Description, TimeField: m.TimeField, Index: idx, DocCount: counts[idx]})
	}
	return c.JSON(http.StatusOK, map[string]any{"models": out})
}

type schemaResponse struct {
	Models    []*schema.Model                  `json:"models"`
	Fields    []*schema.MergedField            `json:"fields"`
	TimeField string                           `json:"timeField,omitempty"`
	Ops       map[schema.FieldType][]schema.Op `json:"opsByType"`
}

func (s *Server) schema(c echo.Context) error {
	var names []string
	if q := strings.TrimSpace(c.QueryParam("models")); q != "" {
		names = strings.Split(q, ",")
	}
	models, _, err := s.registry.Resolve(names)
	if err != nil {
		return &query.BadRequest{Msg: err.Error()}
	}
	fields := schema.Merge(models)
	ops := map[schema.FieldType][]schema.Op{}
	for _, t := range []schema.FieldType{schema.TypeKeyword, schema.TypeText, schema.TypeBoolean, schema.TypeInteger, schema.TypeFloat, schema.TypeDate, schema.TypeIP, schema.TypeMAC, schema.TypeGeoPoint, schema.TypeGeoShape, schema.TypeVector, schema.TypeObject} {
		ops[t] = schema.OpsFor(t)
	}
	tf := ""
	for i, m := range models {
		if i == 0 {
			tf = m.TimeField
		} else if m.TimeField != tf {
			tf = ""
			break
		}
	}
	return c.JSON(http.StatusOK, schemaResponse{Models: models, Fields: fields, TimeField: tf, Ops: ops})
}

func atoi(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int64(r-'0')
	}
	return n
}
