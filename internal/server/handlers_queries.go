package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/prismgroup/query-api/internal/middleware"
	"github.com/prismgroup/query-api/internal/osclient"
	"github.com/prismgroup/query-api/internal/query"
)

type savedQuery struct {
	ID          string          `json:"id"`
	CreatedAt   string          `json:"createdAt"`
	User        string          `json:"user"`
	Indices     []string        `json:"indices"`
	Lucene      string          `json:"lucene,omitempty"`
	Text        string          `json:"text,omitempty"`
	Semantic    string          `json:"semantic,omitempty"`
	FilterCount int             `json:"filterCount"`
	ResultCount int64           `json:"resultCount"`
	TookMs      int64           `json:"tookMs"`
	Request     json.RawMessage `json:"request"`
	Saved       bool            `json:"saved"`
	Name        string          `json:"name,omitempty"`
}

type auditHit struct {
	ID     string `json:"_id"`
	Source struct {
		CreatedAt   string          `json:"created_at"`
		User        string          `json:"user"`
		Indices     []string        `json:"indices"`
		Lucene      string          `json:"lucene"`
		Text        string          `json:"text"`
		Semantic    string          `json:"semantic"`
		FilterCount int             `json:"filter_count"`
		ResultCount int64           `json:"result_count"`
		TookMs      int64           `json:"took_ms"`
		Request     json.RawMessage `json:"request"`
		Saved       bool            `json:"saved"`
		Name        string          `json:"name"`
	} `json:"_source"`
}

func toSaved(h auditHit) savedQuery {
	return savedQuery{
		ID: h.ID, CreatedAt: h.Source.CreatedAt, User: h.Source.User, Indices: h.Source.Indices,
		Lucene: h.Source.Lucene, Text: h.Source.Text, Semantic: h.Source.Semantic, FilterCount: h.Source.FilterCount,
		ResultCount: h.Source.ResultCount, TookMs: h.Source.TookMs, Request: h.Source.Request,
		Saved: h.Source.Saved, Name: h.Source.Name,
	}
}

func (s *Server) listQueries(c echo.Context) error {
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var filters []map[string]any
	switch c.QueryParam("user") {
	case "", "me":
		filters = append(filters, map[string]any{"term": map[string]any{"user": middleware.User(c)}})
	case "all":
	default:
		filters = append(filters, map[string]any{"term": map[string]any{"user": c.QueryParam("user")}})
	}
	if m := c.QueryParam("model"); m != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"indices": m}})
	}
	if c.QueryParam("saved") == "true" {
		filters = append(filters, map[string]any{"term": map[string]any{"saved": true}})
	}
	q := map[string]any{"match_all": map[string]any{}}
	if len(filters) > 0 {
		q = map[string]any{"bool": map[string]any{"filter": filters}}
	}
	body := map[string]any{"size": limit, "query": q, "sort": []map[string]any{{"created_at": "desc"}}}
	var out struct {
		Hits struct {
			Hits []auditHit `json:"hits"`
		} `json:"hits"`
	}
	err := s.os.Do(c.Request().Context(), http.MethodPost, "/"+s.registry.IndexName("queries")+"/_search", body, &out)
	if err != nil {
		var ose *osclient.Error
		if errors.As(err, &ose) && ose.Status == http.StatusNotFound {
			return c.JSON(http.StatusOK, map[string]any{"queries": []savedQuery{}})
		}
		return err
	}
	list := make([]savedQuery, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		list = append(list, toSaved(h))
	}
	return c.JSON(http.StatusOK, map[string]any{"queries": list})
}

func (s *Server) getQuery(c echo.Context) error {
	var h auditHit
	err := s.os.Do(c.Request().Context(), http.MethodGet, "/"+s.registry.IndexName("queries")+"/_doc/"+c.Param("id"), nil, &h)
	if err != nil {
		var ose *osclient.Error
		if errors.As(err, &ose) && ose.Status == http.StatusNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "query not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, toSaved(h))
}

type patchQuery struct {
	Saved *bool   `json:"saved,omitempty"`
	Name  *string `json:"name,omitempty"`
}

// patchQuery updates the user-editable fields of an audited query (save flag, name).
func (s *Server) patchQuery(c echo.Context) error {
	var body patchQuery
	if err := c.Bind(&body); err != nil {
		return &query.BadRequest{Msg: "invalid JSON body"}
	}
	doc := map[string]any{}
	if body.Saved != nil {
		doc["saved"] = *body.Saved
	}
	if body.Name != nil {
		doc["name"] = *body.Name
	}
	if len(doc) == 0 {
		return &query.BadRequest{Msg: "nothing to update"}
	}
	path := "/" + s.registry.IndexName("queries") + "/_update/" + c.Param("id") + "?refresh=true"
	if err := s.os.Do(c.Request().Context(), http.MethodPost, path, map[string]any{"doc": doc}, nil); err != nil {
		var ose *osclient.Error
		if errors.As(err, &ose) && ose.Status == http.StatusNotFound {
			return echo.NewHTTPError(http.StatusNotFound, "query not found")
		}
		return err
	}
	return s.getQuery(c)
}
