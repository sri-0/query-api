// Command seed creates the model indices from the JSON schemas and loads
// generated test data into OpenSearch.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/brianvoe/gofakeit/v7"

	"github.com/prismgroup/query-api/internal/config"
	"github.com/prismgroup/query-api/internal/embed"
	"github.com/prismgroup/query-api/internal/osclient"
	"github.com/prismgroup/query-api/internal/schema"
)

func main() {
	recreate := flag.Bool("recreate", false, "drop and recreate indices before loading")
	logs := flag.Int("logs", 200000, "number of web_logs documents")
	devices := flag.Int("devices", 2000, "number of devices documents")
	alerts := flag.Int("alerts", 20000, "number of alerts documents")
	seed := flag.Uint64("seed", 42, "random seed")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := config.Load()
	check(log, err)
	models, err := schema.LoadDir(cfg.SchemaDir)
	check(log, err)
	reg := schema.NewRegistry(cfg.IndexPrefix, models)
	client, err := osclient.New(cfg.OpenSearchURL, cfg.Username, cfg.Password)
	check(log, err)
	emb, err := embed.New(cfg.EmbedProvider, cfg.EmbedDims, cfg.OllamaURL, cfg.OllamaModel)
	check(log, err)

	ctx := context.Background()
	gofakeit.GlobalFaker = gofakeit.New(*seed)
	rng := rand.New(rand.NewPCG(*seed, *seed))
	gen := &generator{rng: rng, emb: emb, now: time.Now().UTC()}

	for _, m := range reg.Models() {
		idx := reg.IndexName(m.Name)
		if *recreate {
			_ = client.Do(ctx, http.MethodDelete, "/"+idx, nil, nil)
		}
		var exists map[string]any
		if err := client.Do(ctx, http.MethodGet, "/"+idx, nil, &exists); err == nil {
			log.Info("index exists, skipping create", "index", idx)
		} else {
			check(log, client.Do(ctx, http.MethodPut, "/"+idx, m.IndexMapping(), nil))
			log.Info("created index", "index", idx)
		}
	}

	load := func(model string, n int, mk func() map[string]any) {
		idx := reg.IndexName(model)
		start := time.Now()
		check(log, bulk(ctx, client, idx, n, mk, log))
		check(log, client.Do(ctx, http.MethodPost, "/"+idx+"/_refresh", nil, nil))
		log.Info("loaded", "index", idx, "docs", n, "took", time.Since(start).Round(time.Millisecond))
	}
	load("web_logs", *logs, gen.webLog)
	load("devices", *devices, gen.device)
	load("alerts", *alerts, gen.alert)
}

func check(log *slog.Logger, err error) {
	if err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

// bulk streams docs in batches to the _bulk API.
func bulk(ctx context.Context, c *osclient.Client, index string, n int, mk func() map[string]any, log *slog.Logger) error {
	const batch = 2000
	var buf bytes.Buffer
	action := []byte(`{"index":{}}` + "\n")
	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		var resp struct {
			Errors bool `json:"errors"`
			Items  []map[string]struct {
				Error any `json:"error"`
			} `json:"items"`
		}
		if err := c.DoNDJSON(ctx, "/"+index+"/_bulk", buf.Bytes(), &resp); err != nil {
			return err
		}
		if resp.Errors {
			for _, it := range resp.Items {
				for _, v := range it {
					if v.Error != nil {
						b, _ := json.Marshal(v.Error)
						return fmt.Errorf("bulk item error: %s", b)
					}
				}
			}
		}
		buf.Reset()
		return nil
	}
	for i := 0; i < n; i++ {
		b, err := json.Marshal(mk())
		if err != nil {
			return err
		}
		buf.Write(action)
		buf.Write(b)
		buf.WriteByte('\n')
		if (i+1)%batch == 0 {
			if err := flush(); err != nil {
				return err
			}
			if (i+1)%20000 == 0 {
				log.Info("progress", "index", index, "docs", i+1)
			}
		}
	}
	return flush()
}
