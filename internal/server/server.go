// Package server wires the Echo HTTP API.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"github.com/prismgroup/query-api/internal/audit"
	"github.com/prismgroup/query-api/internal/config"
	"github.com/prismgroup/query-api/internal/middleware"
	"github.com/prismgroup/query-api/internal/osclient"
	"github.com/prismgroup/query-api/internal/query"
	"github.com/prismgroup/query-api/internal/schema"
)

// Server holds the handler dependencies.
type Server struct {
	cfg      config.Config
	os       *osclient.Client
	registry *schema.Registry
	builder  *query.Builder
	audit    *audit.Writer
	log      *slog.Logger
	counts   *countCache
}

// New builds the Echo instance with all routes registered.
func New(cfg config.Config, os *osclient.Client, reg *schema.Registry, b *query.Builder, aw *audit.Writer, log *slog.Logger) *echo.Echo {
	s := &Server{cfg: cfg, os: os, registry: reg, builder: b, audit: aw, log: log, counts: newCountCache(30 * time.Second)}
	e := echo.New()
	e.HideBanner = true
	e.HTTPErrorHandler = s.errorHandler
	e.Use(echomw.Recover())
	e.Use(echomw.RequestID())
	e.Use(echomw.CORSWithConfig(echomw.CORSConfig{AllowOrigins: cfg.CORSOrigins, AllowHeaders: []string{"*"}, AllowMethods: []string{"GET", "POST", "PATCH", "OPTIONS"}}))
	e.Use(middleware.Auth())
	e.Use(middleware.RequestLog(log, cfg.LogBodies))
	e.Use(echomw.BodyLimit("1M"))

	e.GET("/healthz", func(c echo.Context) error { return c.JSON(http.StatusOK, map[string]string{"status": "ok"}) })
	e.GET("/readyz", s.ready)

	v1 := e.Group("/v1")
	v1.GET("/models", s.models)
	v1.GET("/schema", s.schema)
	v1.POST("/search/query", s.searchQuery)
	v1.POST("/search/aggregate", s.searchAggregate)
	v1.POST("/search/values", s.searchValues)
	v1.POST("/search/autocomplete", s.searchAutocomplete)
	v1.POST("/search/validate", s.searchValidate)
	v1.GET("/queries", s.listQueries)
	v1.GET("/queries/:id", s.getQuery)
	v1.PATCH("/queries/:id", s.patchQuery)
	return e
}

type apiError struct {
	Error   string `json:"error"`
	Details any    `json:"details,omitempty"`
}

func (s *Server) errorHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	var br *query.BadRequest
	var he *echo.HTTPError
	var ose *osclient.Error
	switch {
	case errors.As(err, &br):
		_ = c.JSON(http.StatusBadRequest, apiError{Error: br.Msg})
	case errors.As(err, &he):
		_ = c.JSON(he.Code, apiError{Error: http.StatusText(he.Code), Details: he.Message})
	case errors.As(err, &ose):
		status := http.StatusBadGateway
		if ose.Status == http.StatusBadRequest || ose.Status == http.StatusUnauthorized || ose.Status == http.StatusForbidden {
			status = ose.Status
		}
		_ = c.JSON(status, apiError{Error: "opensearch error", Details: ose.Body})
	default:
		s.log.Error("unhandled error", "err", err)
		_ = c.JSON(http.StatusInternalServerError, apiError{Error: "internal error"})
	}
}

func (s *Server) ready(c echo.Context) error {
	var out map[string]any
	if err := s.os.Do(c.Request().Context(), http.MethodGet, "/_cluster/health", nil, &out); err != nil {
		return c.JSON(http.StatusServiceUnavailable, apiError{Error: "opensearch unavailable", Details: err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]any{"status": "ok", "opensearch": out["status"]})
}
