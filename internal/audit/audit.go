// Package audit records executed queries in the queries index.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prismgroup/query-api/internal/osclient"
)

// Entry is one audited query.
type Entry struct {
	ID          string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	User        string    `json:"user"`
	Indices     []string  `json:"indices"`
	Lucene      string    `json:"lucene,omitempty"`
	Text        string    `json:"text,omitempty"`
	Semantic    string    `json:"semantic,omitempty"`
	FilterCount int       `json:"filter_count"`
	Request     any       `json:"request"`
	ResultCount int64     `json:"result_count"`
	TookMs      int64     `json:"took_ms"`
	Client      string    `json:"client,omitempty"`
}

// Writer indexes entries asynchronously so it never delays a response.
type Writer struct {
	client *osclient.Client
	index  string
	ch     chan Entry
	log    *slog.Logger
}

// NewWriter starts the background indexer.
func NewWriter(client *osclient.Client, index string, log *slog.Logger) *Writer {
	w := &Writer{client: client, index: index, ch: make(chan Entry, 1024), log: log}
	go w.run()
	return w
}

// EnsureIndex creates the audit index when missing and pushes the current
// mapping so fields added later (e.g. `saved`) exist without a reseed.
func EnsureIndex(ctx context.Context, client *osclient.Client, index string, body map[string]any) error {
	if err := client.Do(ctx, http.MethodGet, "/"+index, nil, nil); err != nil {
		var ose *osclient.Error
		if !errors.As(err, &ose) || ose.Status != http.StatusNotFound {
			return err
		}
		return client.Do(ctx, http.MethodPut, "/"+index, body, nil)
	}
	mappings, _ := body["mappings"].(map[string]any)
	return client.Do(ctx, http.MethodPut, "/"+index+"/_mapping", mappings, nil)
}

// NewID returns a random 20-hex-char id.
func NewID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Record enqueues an entry; it drops (and logs) if the queue is full.
func (w *Writer) Record(e Entry) {
	select {
	case w.ch <- e:
	default:
		w.log.Warn("audit queue full, dropping entry", "id", e.ID)
	}
}

func (w *Writer) run() {
	for e := range w.ch {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := w.client.Do(ctx, http.MethodPut, "/"+w.index+"/_doc/"+e.ID, e, nil)
		cancel()
		if err != nil {
			w.log.Error("audit write failed", "id", e.ID, "err", err)
		}
	}
}
