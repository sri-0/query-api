package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/prismgroup/query-api/internal/audit"
	"github.com/prismgroup/query-api/internal/middleware"
	"github.com/prismgroup/query-api/internal/query"
	"github.com/prismgroup/query-api/internal/schema"
)

func (s *Server) searchQuery(c echo.Context) error {
	var req query.Request
	if err := c.Bind(&req); err != nil {
		return &query.BadRequest{Msg: "invalid JSON body"}
	}
	ctx := c.Request().Context()
	plan, err := s.builder.Resolve(ctx, &req, true)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := s.os.Do(ctx, http.MethodPost, "/"+strings.Join(plan.Indices, ",")+"/_search", plan.Body, &raw); err != nil {
		return err
	}
	var total int64
	if req.WantMeta() {
		total = s.totalCount(c, plan.Indices)
	}
	resp, err := s.builder.Decode(plan, &req, raw, total)
	if err != nil {
		return err
	}
	if resp.Meta != nil {
		id := audit.NewID()
		resp.Meta.QueryID = id
		names := make([]string, len(plan.Models))
		for i, m := range plan.Models {
			names[i] = m.Name
		}
		sem := ""
		if req.Semantic != nil {
			sem = req.Semantic.Text
		}
		s.audit.Record(audit.Entry{
			ID: id, CreatedAt: time.Now().UTC(), User: middleware.User(c), Indices: names,
			Lucene: req.Lucene, Text: req.Text, Semantic: sem, FilterCount: len(req.Filters),
			Request: req, ResultCount: resp.Meta.FilterRowCount, TookMs: resp.Meta.TookMs, Client: c.RealIP(),
		})
	}
	return c.JSON(http.StatusOK, resp)
}

func (s *Server) searchAggregate(c echo.Context) error {
	var req query.Request
	if err := c.Bind(&req); err != nil {
		return &query.BadRequest{Msg: "invalid JSON body"}
	}
	t := true
	req.Meta = &t
	ctx := c.Request().Context()
	plan, err := s.builder.Resolve(ctx, &req, false)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := s.os.Do(ctx, http.MethodPost, "/"+strings.Join(plan.Indices, ",")+"/_search?request_cache=true", plan.Body, &raw); err != nil {
		return err
	}
	meta, err := s.builder.DecodeMeta(plan, &req, raw, s.totalCount(c, plan.Indices))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"meta": meta})
}

func (s *Server) searchValues(c echo.Context) error       { return s.values(c, false) }
func (s *Server) searchAutocomplete(c echo.Context) error { return s.values(c, true) }

func (s *Server) values(c echo.Context, autocomplete bool) error {
	var req query.ValuesRequest
	if err := c.Bind(&req); err != nil {
		return &query.BadRequest{Msg: "invalid JSON body"}
	}
	if req.Field == "" {
		return &query.BadRequest{Msg: "field is required"}
	}
	ctx := c.Request().Context()
	f := false
	base := &query.Request{Indices: req.Indices, Filters: req.Filters, Lucene: req.Lucene, Text: req.Text, Meta: &f}
	plan, err := s.builder.Resolve(ctx, base, false)
	if err != nil {
		return err
	}
	fld := schema.Lookup(plan.Fields, req.Field)
	if fld == nil {
		return &query.BadRequest{Msg: "unknown field " + req.Field}
	}
	if !fld.Aggregatable || fld.Conflict {
		return &query.BadRequest{Msg: "field " + req.Field + " is not aggregatable"}
	}
	body, err := s.builder.ValuesBody(plan, fld, &req, autocomplete)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := s.os.Do(ctx, http.MethodPost, "/"+strings.Join(plan.Indices, ",")+"/_search?request_cache=true", body, &raw); err != nil {
		return err
	}
	out, err := query.DecodeValues(req.Field, raw, req.Size)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) searchValidate(c echo.Context) error {
	var req query.ValidateRequest
	if err := c.Bind(&req); err != nil {
		return &query.BadRequest{Msg: "invalid JSON body"}
	}
	ctx := c.Request().Context()
	f := false
	plan, err := s.builder.Resolve(ctx, &query.Request{Indices: req.Indices, Lucene: req.Lucene, Meta: &f}, false)
	if err != nil {
		return err
	}
	var out struct {
		Valid        bool `json:"valid"`
		Explanations []struct {
			Index       string `json:"index"`
			Valid       bool   `json:"valid"`
			Error       string `json:"error"`
			Explanation string `json:"explanation"`
		} `json:"explanations"`
	}
	body := map[string]any{"query": plan.Body["query"]}
	if err := s.os.Do(ctx, http.MethodPost, "/"+strings.Join(plan.Indices, ",")+"/_validate/query?explain=true", body, &out); err != nil {
		return err
	}
	resp := map[string]any{"valid": out.Valid}
	for _, e := range out.Explanations {
		if !e.Valid {
			resp["error"] = parseError(e.Error)
			break
		}
	}
	return c.JSON(http.StatusOK, resp)
}

// totalCount returns the unfiltered document count across indices (cached briefly).
func (s *Server) totalCount(c echo.Context, indices []string) int64 {
	key := strings.Join(indices, ",")
	if v, ok := s.counts.get(key); ok {
		return v
	}
	var out struct {
		Count int64 `json:"count"`
	}
	if err := s.os.Do(c.Request().Context(), http.MethodGet, "/"+key+"/_count", nil, &out); err != nil {
		return 0
	}
	s.counts.set(key, out.Count)
	return out.Count
}

// parseError reduces OpenSearch's nested exception text to the Lucene parser message.
func parseError(s string) string {
	const marker = "ParseException[Cannot parse "
	if i := strings.Index(s, marker); i >= 0 {
		s = "Cannot parse " + s[i+len(marker):]
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(strings.TrimSpace(s), ";]")
}
