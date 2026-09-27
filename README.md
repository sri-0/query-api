# query-api

Typed query API over OpenSearch. Models are JSON Schema files in `schemas/` (one per index);
the index name is `${OS_INDEX_PREFIX}${model}`.

## Endpoints (`/v1`)

| Method | Path | Body / params |
|---|---|---|
| GET | `/models` | |
| GET | `/schema?models=a,b` | merged fields, per-field ops, conflicts |
| POST | `/search/query` | `indices, lucene, text, semantic, filters, sort, size, cursor, histogram, facets, fields, meta` |
| POST | `/search/aggregate` | same body, aggregations only |
| POST | `/search/values` | `indices, field, prefix, size, after, filters` |
| POST | `/search/autocomplete` | `indices, field, q, size, filters` |
| POST | `/search/validate` | `indices, lucene` |
| GET | `/queries`, `/queries/{id}` | audit of executed queries |

Filter ops by field type live in `internal/schema/ops.go`; translation to OpenSearch DSL in
`internal/query/filters.go`. Responses match the data-table-filters `InfiniteQueryResponse` shape.

## Config (`.env`)

`OS_URL`, `OS_INDEX_PREFIX`, `OS_USERNAME`/`OS_PASSWORD` (optional), `SCHEMA_DIR`, `TRACK_TOTAL_HITS`
(`true`, `false` or a threshold), `EMBED_PROVIDER` (`fake` | `ollama`), `EMBED_DIMS`, `OLLAMA_URL`,
`OLLAMA_MODEL`, `LOG_BODIES`, `CORS_ORIGINS`, `PORT`.

An incoming `Authorization` header is forwarded to OpenSearch unchanged; its claims (unverified) name
the user in request logs and the `queries` audit index.

## Commands

```sh
go run ./cmd/query-api
go run ./cmd/seed --recreate [--logs 200000 --devices 2000 --alerts 20000 --seed 42]
```

## Adding a model

Drop `schemas/<name>.schema.json` (Pydantic JSON Schema export). Optional extensions:
`x-time-field`, `x-internal`, per property `x-opensearch` (`type`, `dimension`, ...) and
`x-ui` (`label`, `cell`, `hidden`, `defaultVisible`). Run the seeder (or create the index from
`Model.IndexMapping()`); the API and UI pick it up on restart.
