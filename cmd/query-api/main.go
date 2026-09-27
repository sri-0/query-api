// Command query-api serves the generic OpenSearch query API.
package main

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/prismgroup/query-api/internal/audit"
	"github.com/prismgroup/query-api/internal/config"
	"github.com/prismgroup/query-api/internal/embed"
	"github.com/prismgroup/query-api/internal/osclient"
	"github.com/prismgroup/query-api/internal/query"
	"github.com/prismgroup/query-api/internal/schema"
	"github.com/prismgroup/query-api/internal/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		fatal(log, "config", err)
	}
	models, err := schema.LoadDir(cfg.SchemaDir)
	if err != nil {
		fatal(log, "schemas", err)
	}
	reg := schema.NewRegistry(cfg.IndexPrefix, models)
	client, err := osclient.New(cfg.OpenSearchURL, cfg.Username, cfg.Password)
	if err != nil {
		fatal(log, "opensearch client", err)
	}
	emb, err := embed.New(cfg.EmbedProvider, cfg.EmbedDims, cfg.OllamaURL, cfg.OllamaModel)
	if err != nil {
		fatal(log, "embedder", err)
	}
	builder := &query.Builder{Registry: reg, Embedder: emb, TrackTotalHits: trackTotalHits(cfg.TrackTotalHits), MaxSize: 500}
	aw := audit.NewWriter(client, reg.IndexName("queries"), log)

	e := server.New(cfg, client, reg, builder, aw, log)
	log.Info("listening", "port", cfg.Port, "prefix", cfg.IndexPrefix, "models", len(reg.Public()), "embedder", cfg.EmbedProvider)
	if err := e.Start(":" + cfg.Port); err != nil {
		fatal(log, "server", err)
	}
}

func trackTotalHits(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return true
}

func fatal(log *slog.Logger, what string, err error) {
	log.Error(what, "err", err)
	os.Exit(1)
}
