// Package osclient wraps the opensearch-go client with a small JSON-oriented
// API and per-request Authorization forwarding.
package osclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v5"
)

type ctxKey struct{}

// WithAuthorization stores an incoming Authorization header in ctx so the
// transport forwards it to OpenSearch.
func WithAuthorization(ctx context.Context, header string) context.Context {
	if header == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, header)
}

// Error is a non-2xx response from OpenSearch.
type Error struct {
	Status int
	Body   json.RawMessage
}

func (e *Error) Error() string {
	return fmt.Sprintf("opensearch: status %d: %s", e.Status, truncate(e.Body, 500))
}

// Client issues raw JSON requests to OpenSearch.
type Client struct {
	os *opensearch.Client
}

// New builds a client. Username/password are optional service credentials used
// only when the request context carries no Authorization header.
func New(url, username, password string) (*Client, error) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	noDiscovery := false // the configured address is the entry point (load balancer or container port map)
	c, err := opensearch.NewClient(opensearch.Config{
		Addresses:            []string{url},
		Username:             username,
		Password:             password,
		Transport:            &forwardAuth{next: base},
		DiscoverNodesOnStart: &noDiscovery,
	})
	if err != nil {
		return nil, err
	}
	return &Client{os: c}, nil
}

// Do sends method path with an optional JSON body and decodes the response into out.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.os.Request(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Body: raw}
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// DoNDJSON sends a newline-delimited body (bulk API).
func (c *Client) DoNDJSON(ctx context.Context, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := c.os.Request(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Body: raw}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// forwardAuth replaces the Authorization header with the caller's token when
// one is present in the request context.
type forwardAuth struct{ next http.RoundTripper }

func (t *forwardAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	if h, ok := req.Context().Value(ctxKey{}).(string); ok && h != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", h)
	}
	return t.next.RoundTrip(req)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
