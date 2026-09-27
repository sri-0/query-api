package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"time"

	"github.com/labstack/echo/v4"
)

// RequestLog writes one structured audit line per request.
func RequestLog(log *slog.Logger, logBodies bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			req := c.Request()
			var body []byte
			if logBodies && req.Body != nil {
				body, _ = io.ReadAll(req.Body)
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
			err := next(c)
			if err != nil {
				c.Error(err)
			}
			attrs := []any{
				"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
				"user", User(c),
				"method", req.Method,
				"path", req.URL.Path,
				"status", c.Response().Status,
				"duration_ms", time.Since(start).Milliseconds(),
				"bytes_out", c.Response().Size,
				"ip", c.RealIP(),
				"ua", req.UserAgent(),
			}
			if roles, ok := c.Get(ctxRoles).([]string); ok && len(roles) > 0 {
				attrs = append(attrs, "roles", roles)
			}
			if logBodies && len(body) > 0 {
				attrs = append(attrs, "body", string(body))
			}
			if err != nil {
				attrs = append(attrs, "err", err.Error())
			}
			log.Info("request", attrs...)
			return nil
		}
	}
}
