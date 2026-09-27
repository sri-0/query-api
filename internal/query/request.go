// Package query translates typed QueryRequests into OpenSearch DSL and
// OpenSearch responses into the shape the UI consumes.
package query

import (
	"encoding/json"
	"fmt"

	"github.com/prismgroup/query-api/internal/schema"
)

// Filter is one typed predicate on a field.
type Filter struct {
	Field string          `json:"field"`
	Op    schema.Op       `json:"op"`
	Value json.RawMessage `json:"value,omitempty"`
}

// Sort is one sort clause.
type Sort struct {
	Field string `json:"field"`
	Order string `json:"order,omitempty"` // asc | desc
}

// Semantic requests k-NN search using an embedded query string.
type Semantic struct {
	Text string `json:"text"`
	K    int    `json:"k,omitempty"`
}

// Histogram requests a date_histogram over the time field.
type Histogram struct {
	Field    string `json:"field,omitempty"`
	Interval string `json:"interval,omitempty"` // auto | 1m | 5m | 1h | 1d ...
	Series   string `json:"series,omitempty"`   // keyword field to split by (or "_model")
}

// Request is the body of POST /v1/search/query and /v1/search/aggregate.
type Request struct {
	Indices   []string   `json:"indices,omitempty"`
	Lucene    string     `json:"lucene,omitempty"`
	Text      string     `json:"text,omitempty"`
	Semantic  *Semantic  `json:"semantic,omitempty"`
	Filters   []Filter   `json:"filters,omitempty"`
	Sort      []Sort     `json:"sort,omitempty"`
	Size      int        `json:"size,omitempty"`
	Cursor    string     `json:"cursor,omitempty"`
	Direction string     `json:"direction,omitempty"` // next | prev
	Histogram *Histogram `json:"histogram,omitempty"`
	Facets    []string   `json:"facets,omitempty"`
	Fields    []string   `json:"fields,omitempty"`
	Meta      *bool      `json:"meta,omitempty"`
}

// WantMeta reports whether aggregations and counts were requested (default true).
func (r *Request) WantMeta() bool { return r.Meta == nil || *r.Meta }

// ValuesRequest is the body of POST /v1/search/values and /autocomplete.
type ValuesRequest struct {
	Indices []string `json:"indices,omitempty"`
	Field   string   `json:"field"`
	Prefix  string   `json:"prefix,omitempty"` // values: case-sensitive prefix
	Query   string   `json:"q,omitempty"`      // autocomplete: case-insensitive contains
	Size    int      `json:"size,omitempty"`
	After   string   `json:"after,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
	Lucene  string   `json:"lucene,omitempty"`
	Text    string   `json:"text,omitempty"`
}

// ValidateRequest is the body of POST /v1/search/validate.
type ValidateRequest struct {
	Indices []string `json:"indices,omitempty"`
	Lucene  string   `json:"lucene"`
}

// BadRequest is a client error with a stable message.
type BadRequest struct{ Msg string }

func (e *BadRequest) Error() string { return e.Msg }

func badf(format string, a ...any) error { return &BadRequest{Msg: fmt.Sprintf(format, a...)} }
