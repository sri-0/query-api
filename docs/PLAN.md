# table-ui: query-api + query-ui plan

Two repos under `~/code/table-ui/`, plus shared local infra:

```
table-ui/
├── PLAN.md                 # this file
├── infra/                  # docker-compose: OpenSearch 3.8 + Dashboards, JWT config
├── query-api/              # Go 1.26 + Echo + opensearch-go v5
└── query-ui/               # Next.js 16 + shadcn + data-table-filters + AI Elements
```

Versions verified 2026-09-28: Go 1.26.0 (auto-downloads via go.mod toolchain directive; Homebrew has 1.25.1),
opensearch-go v5.0.0, OpenSearch 3.8.0 (security, k-NN, neural-search, ml-commons, geospatial bundled),
Next.js 16.2, React 19.2, @tanstack/react-table 9, @tanstack/react-query 5, nuqs 2, shadcn CLI 4.21
(Tailwind v4 default), ai 7 / @ai-sdk/react 4 / @ai-sdk/anthropic 4, ai-elements 1.9,
openstatus data-table-filters registry `@data-table-filters/*`.

---

## 0. Infra (`infra/`)

- `docker-compose.yml`: `opensearchproject/opensearch:3.8.0` single node with
  `DISABLE_SECURITY_PLUGIN=true` (plain http://localhost:9200, no auth), `opensearch-dashboards:3.8.0`
  with `DISABLE_SECURITY_DASHBOARDS_PLUGIN=true`. No JWT setup locally. In deployed environments the API
  simply forwards whatever `Authorization` header it receives (section 1.8); nothing else changes.
- `Makefile` at root: `make up`, `make seed`, `make api`, `make ui`.
- The seeder lives in query-api (`cmd/seed`) so it reuses the mapping generator.

## 1. query-api (Go)

### 1.1 Stack
Go 1.26, `github.com/labstack/echo/v4`, `github.com/opensearch-project/opensearch-go/v5`
(`opensearchapi`, `opensearchtransport`), `log/slog` JSON logging, `github.com/golang-jwt/jwt/v5`
(parse only, no verification required for passthrough; verification optional via JWKS/secret),
`github.com/santhosh-tekuri/jsonschema/v6` for JSONSchema loading, `github.com/brianvoe/gofakeit/v7`
for seed data, `github.com/oapi-codegen/oapi-codegen` to generate Echo server + TS client from
`api/openapi.yaml`.

### 1.2 Layout
```
query-api/
├── cmd/query-api/main.go
├── cmd/seed/main.go                 # creates indexes from schemas, bulk loads fake data
├── api/openapi.yaml                 # source of truth; generates Go server types + TS client for UI
├── schemas/                         # JSONSchema per index (exported from Pydantic), x-opensearch extensions
│   ├── web_logs.schema.json
│   ├── devices.schema.json
│   └── queries.schema.json
├── internal/
│   ├── config/                      # env: OS_URL, OS_INDEX_PREFIX, OS_USER/PASS (optional), EMBED_PROVIDER, TRACK_TOTAL_HITS...
│   ├── server/                      # echo wiring, routes, error mapping
│   ├── middleware/
│   │   ├── requestlog.go            # audit log: request id, user (jwt sub), method, path, index, status, latency, bytes
│   │   ├── auth.go                  # copy Authorization header + parsed (unverified) claims into ctx; never required
│   │   └── requestid.go
│   ├── osclient/
│   │   ├── client.go                # opensearchapi.NewClient; cloned http.Transport
│   │   └── transport.go             # RoundTripper: forward Authorization from ctx if present; else optional service creds
│   ├── schema/
│   │   ├── model.go                 # normalized Field{Name,Type,Ops,Sortable,Aggregatable,Enum,Description,Format,SubFields}
│   │   ├── jsonschema.go            # JSONSchema -> []Field (uses x-opensearch: {type, analyzer, dims, ...})
│   │   ├── mapping.go               # GET _mapping -> []Field (fallback / drift check)
│   │   ├── registry.go              # model name (=index without prefix) -> schema; merged cross-model view; conflicts
│   │   ├── indices.go               # model names <-> prefixed OS index names; default = all models
│   │   └── ops.go                   # FieldType -> allowed filter ops table (below)
│   ├── query/
│   │   ├── request.go               # QueryRequest / Filter types (+ validation against schema)
│   │   ├── builder.go               # QueryRequest -> OpenSearch bool query
│   │   ├── filters_*.go             # per-type translators: term, range, ip/cidr, geo, text, bool, exists
│   │   ├── lucene.go                # query_string wrapper (fields allowlist, lenient, validate)
│   │   ├── semantic.go              # embed -> knn clause (with filter), or neural clause
│   │   ├── cursor.go                # search_after <-> opaque base64 cursor; tiebreaker on _id
│   │   ├── aggs.go                  # histogram (date_histogram auto interval), facets (terms), values; shared by /query and /aggregate
│   │   └── response.go              # -> data-table-filters InfiniteQueryResponse shape
│   ├── embed/                       # Embedder interface; anthropic-compatible / openai / ollama / fake
│   ├── autocomplete/                # prefix terms agg, search_as_you_type, completion suggester
│   ├── values/                      # single-field distinct values (terms / composite agg, 30s cache)
│   └── audit/                       # writes saved query doc to `queries` index (async, buffered)
└── internal/testutil/               # testcontainers OpenSearch for integration tests
```

### 1.3 Endpoints (all JSON, versioned under `/v1`, indices only ever in POST bodies)
| Method | Path | Purpose |
|---|---|---|
| GET | `/v1/models` | Models available (name, title, description, time field, doc count). Model = JSONSchema = one index. |
| GET | `/v1/schema?models=a,b` | Normalized fields for the given models (default all): per-model fields plus merged view with conflict flags |
| POST | `/v1/search/query` | Hits + optional meta (counts, histogram, facets). Body `indices` optional; default all models under the prefix |
| POST | `/v1/search/aggregate` | Aggregations only (histogram, facets) for the same body shape; no hits |
| POST | `/v1/search/values` | Distinct values of one field with counts; body: `indices, field, prefix, size, after, filters?` |
| POST | `/v1/search/autocomplete` | Value suggestions for search bar / dropdowns; body: `indices, field, q, size, filters?` |
| POST | `/v1/search/validate` | Validate a Lucene string via `_validate/query?explain`; body: `indices, lucene` |
| GET | `/v1/queries`, `/v1/queries/{id}` | Saved query audit (recent first, filtered to caller) |
| GET | `/healthz`, `/readyz` | Liveness / OpenSearch ping |

Values/autocomplete are POST so the indices list, field and the tab's current `filters` (for contextual options)
travel in the body; OpenSearch's shard request cache still caches the underlying `size: 0` aggregations.

**Index resolution.** `OS_INDEX_PREFIX` (env, differs per environment; `ds_` locally) is prepended server-side. A request without `indices`
searches `ds_*` minus internal indices (`ds_queries`). `indices: ["web_logs","devices"]` becomes
`ds_web_logs,ds_devices`. Model names are validated against the schema registry, so callers can never
address an index outside the prefix. Every hit carries `_index` mapped back to its model name (`"_model"`) so
the UI can show which model a row came from.

**Cross-model querying.** Filters on a field only some models have are fine: OpenSearch treats the field as
unmapped and those indices return no hits for that clause. Sorting uses `unmapped_type` from the schema so
mixed result sets sort without errors. The one real hazard is the same field name mapped with different types
in two models (e.g. `port` keyword vs integer); the registry detects this at load and the schema endpoint marks
the field `conflict: true` with per-model types, and `/query` rejects a filter on it unless `indices` narrows to
compatible models. Since the schemas are generated from your Pydantic models, this is a lint check you can
also run in CI (`query-api schema check`).

HTTP QUERY method: OpenSearch itself doesn't support it and Echo has no first-class routing for it,
so we use POST.

### 1.4 Query request
```jsonc
{
  "indices": ["web_logs"],                              // optional model names; default = all models under prefix
  "lucene": "status:>=500 AND host:web-*",              // optional, query_string
  "text": "login failure",                              // optional, multi_match over text fields
  "semantic": {"text": "users locked out", "k": 100},   // optional, embeds then knn on schema's vector field
  "filters": [
    {"field": "level",     "op": "in",           "value": ["error","warn"]},
    {"field": "timestamp", "op": "between",      "value": ["2026-09-01T00:00:00Z", "now"]},
    {"field": "bytes",     "op": "gte",          "value": 1024},
    {"field": "src_ip",    "op": "cidr",         "value": "10.0.0.0/8"},
    {"field": "mac",       "op": "prefix",       "value": "00:1a:2b"},
    {"field": "location",  "op": "geo_distance", "value": {"lat": -33.86, "lon": 151.2, "distance": "25km"}},
    {"field": "message",   "op": "match",        "value": "timeout"},
    {"field": "active",    "op": "eq",           "value": true},
    {"field": "trace_id",  "op": "exists"}
  ],
  "sort": [{"field": "timestamp", "order": "desc"}],
  "size": 50,
  "cursor": "eyJ...",                  // opaque search_after; omitted for first page
  "direction": "next",                 // next | prev (matches data-table-filters bidirectional cursor)
  "histogram": {"field": "timestamp", "interval": "auto", "series": "level"},
  "facets": ["level", "region", "method"],
  "fields": ["timestamp", "level", "message"],
  "meta": true                         // false on pagination requests (skip aggs/counts entirely)
}
```
Response (matches data-table-filters `InfiniteQueryResponse` so the table needs no adapter):
```jsonc
{ "data": [{"_model": "web_logs", "_id": "...", ...}],
  "meta": { "totalRowCount": 1204411, "filterRowCount": 8821, "filterRowCountRelation": "eq", "tookMs": 31,
  "chartData": [{"timestamp": 1758000000000, "error": 3, "warn": 10, "info": 40}],
  "facets": {"level": {"rows": [{"value":"error","total":3}], "total": 53}, "bytes": {"min": 0, "max": 9e6}},
  "queryId": "01J..." }, "nextCursor": "...", "prevCursor": null }
```

### 1.4a Counting beyond 10,000
By default `hits.total` stops at 10,000 and reports `{"value": 10000, "relation": "gte"}`; that is the
`track_total_hits` default, not `index.max_result_window` (which only limits `from + size` paging, and our
`search_after` cursor is not subject to it). Options:
- `track_total_hits: true` -> exact count, always. OpenSearch must visit every matching doc instead of early-
  terminating, so on a 100M-doc index a broad query costs a few hundred ms more; on our scale it is negligible.
- `track_total_hits: <int>` (e.g. 1,000,000) -> exact up to the threshold, then `gte`. Good middle ground.
- `_count` API -> exact, cheaper than a search because it skips scoring/fetching. Cached per shard.
Plan: `TRACK_TOTAL_HITS` env (default `true`) applied only on `meta: true` requests; pagination requests set
`track_total_hits: false`. `filterRowCountRelation` (`eq|gte`) is returned so the UI can render "10,000+" when a
threshold is used. `totalRowCount` (unfiltered, per model set) comes from `_count` cached 30s.

### 1.4b Separate aggregate API or same request?
Both, deliberately:
- **Same request for the first page** (`meta: true`). One shard pass computes hits + `date_histogram` + facet
  `terms` aggs together, so it is cheaper than two requests and the chart, facet counts and rows are guaranteed to
  reflect the same filter state at the same instant. This is also what data-table-filters expects (meta on page 1,
  `_meta=false` afterwards).
- **`POST /v1/search/aggregate` for everything else**: changing histogram interval/series field, live/auto-refresh of
  the chart without refetching rows, facet refresh after a filter change while rows are cached, and the AI panel
  asking "how many by region". Aggregation-only requests use `size: 0`, and OpenSearch's shard request cache
  caches `size: 0` responses automatically (it never caches requests with hits), so repeated chart refreshes
  are near free. `/values` is a thin wrapper over the same aggregation builder.
The UI calls `/aggregate` only from chart/facet controls; the table's infinite query never does.

### 1.5 Field type -> allowed ops (`schema/ops.go`)
| Normalized type | OpenSearch mapping | Ops | UI filter |
|---|---|---|---|
| keyword / enum | keyword | eq, ne, in, not_in, prefix, wildcard, exists | checkbox (options from schema enum or values API) |
| text | text (+ .keyword subfield) | match, match_phrase, wildcard(on .keyword), exists | input |
| boolean | boolean | eq, exists | checkbox true/false |
| integer / float | long/integer/double/float | eq, ne, in, gt, gte, lt, lte, between, exists | slider (min/max from facets) |
| date | date | between, gt, gte, lt, lte, exists (accepts ISO, epoch ms, date math) | timerange |
| ip | ip | eq, in, cidr, between, exists | input with IP/CIDR validation |
| mac | keyword (+ normalizer lowercase) | eq, in, prefix, exists | input with MAC mask |
| geo_point | geo_point | geo_distance, geo_bounding_box, geo_polygon, exists | map/coords input |
| geo_shape | geo_shape | geo_shape(intersects/within), exists | geojson input |
| vector | knn_vector (dims from schema) | semantic only (not filterable/sortable) | hidden; drives semantic search |
| object / nested | object / nested | flattened dotted paths; nested wrapped in `nested` query | per leaf type |

Sortable = every type except text (unless .keyword), geo, vector. Aggregatable = keyword, number, date, ip, boolean.
The schema endpoint emits this table per field so the UI never hardcodes it.

### 1.6 Abstraction decision: how much of OpenSearch to hide
Options considered:
- **A. Typed filter DSL (chosen).** Small, validated vocabulary above; UI generated from it; safe to expose to browsers; audit-friendly. Cost: every new capability needs a translator (~30 lines each).
- **B. Raw OpenSearch DSL passthrough** with an allowlist of query types. Fastest to build, hardest to keep safe and the UI still needs a typed model to render filters.
- **C. Hybrid.** A + an optional `raw` clause (`{"raw": {...}}` merged into `bool.filter`) gated by a role claim, for power users and the AI agent.
Recommendation: A now, add C's `raw` escape hatch behind `roles: ["query_power"]` later. Lucene via `query_string` already gives power users most of B.

### 1.7 Semantic search
`Embedder` interface (`Embed(ctx, []string) ([][]float32, error)`). Implementations: `anthropic`-compatible
HTTP (configurable base URL/model since Anthropic's own API has no embeddings endpoint; Voyage is the documented
partner), `openai`, `ollama` (`nomic-embed-text`, 768 dims, free local default), `fake` (hash-based, for tests/seed).
Query path: embed text -> `knn` query on the schema's vector field with `filter` = the rest of the bool query
(efficient k-NN filtering, Lucene engine, HNSW). Alternative documented but not chosen: OpenSearch `neural` query
with an ml-commons deployed model (keeps embeddings in-cluster; heavier local setup). Seeder must use the same
embedder as the API so vectors match; default `fake` for speed, `ollama` when available.

### 1.8 Auth, audit, logging
- `auth.go`: if an `Authorization` header is present it is copied into the request context and forwarded verbatim
  to OpenSearch by `transport.go`; claims are parsed (not verified) only to get `sub`/`roles` for audit. If absent,
  the client uses optional `OS_USERNAME/OS_PASSWORD`, or nothing (local compose has security disabled). No JWT
  configuration exists in local infra.
- `requestlog.go`: one slog JSON line per request (`request_id, user, roles, method, path, index, status,
  duration_ms, bytes_out, ip, ua`). Bodies not logged by default (`LOG_BODIES=true` for dev).
- `audit/`: after each `/query`, write `{id, user, index, request (full body), lucene, filter_count, result_count,
  took_ms, created_at, client, ui_state?}` to `queries` index via a buffered async writer (never blocks the
  response). `queryId` returned in meta. `GET /queries` reads it, newest first, filtered to the caller's `sub`
  unless role admin (when there are no claims, `user` is `anonymous`).

### 1.9 Schema source
Primary: JSONSchema files in `schemas/` (Pydantic export), one file per model; the file's `title` (or filename) is
the model name and the index is `${OS_INDEX_PREFIX}${model}`. Extension conventions: `x-opensearch` object per property
(`{"type":"ip"}`, `{"type":"knn_vector","dimension":768}`, `{"type":"keyword","normalizer":"lowercase"}`),
`enum` for options, `description` for tooltips, `format: date-time|ipv4|ipv6` as type hints when `x-opensearch`
absent. `x-ui` optional (`{"label","hidden","defaultVisible","cell":"badge|status-code|level|timestamp"}`), which
maps straight onto data-table-filters cell renderers. The seeder generates index mappings from the same files.
Fallback: `_mapping` when no JSONSchema exists; `/schema` response reports `source: jsonschema|mapping` and drift
warnings when both exist and disagree.

### 1.10 Test data (`cmd/seed`)
The seeder reads the same `OS_INDEX_PREFIX` as the API (local `.env`: `ds_`) and creates one index per schema
model. Three data models plus the audit index, so multi-index result sets are exercised from day one. Fields
are deliberately overlapping (`timestamp`, `host`, `ip`/`src_ip`, `mac`, `location`, `message`, `embedding`)
and partially disjoint (`status`/`bytes` only in web_logs, `vendor`/`open_ports` only in devices,
`severity`/`rule_id` only in alerts) so the UI has to cope with sparse columns.
- `${PREFIX}web_logs` (default 200k docs, `--count`): `timestamp` (last 30 days, diurnal skew), `level` enum
  (debug/info/warn/error), `method`, `status` (weighted), `path`, `host` (`web-01..web-12`), `region` enum,
  `src_ip` ip (mix of 10/8, 192.168/16, public), `dst_ip`, `mac` keyword, `bytes` long, `latency_ms` float,
  `user_agent` text, `message` text (templated sentences so semantic search is meaningful), `tags` keyword[],
  `active` boolean, `location` geo_point (clustered around 6 cities), `trace_id` keyword, `embedding` knn_vector
  of `message`.
- `${PREFIX}devices` (2k docs): `hostname`, `ip`, `mac`, `vendor` enum, `os`, `first_seen`, `last_seen`, `location`,
  `open_ports` integer[], `notes` text, `embedding`.
- `${PREFIX}alerts` (20k docs): `timestamp`, `severity` enum (low/medium/high/critical), `rule_id` keyword,
  `rule_name`, `host`, `src_ip`, `dst_ip`, `dst_port` integer, `mac`, `message` text, `acknowledged` boolean,
  `location` geo_point, `tags`, `embedding`.
- `${PREFIX}queries`: created empty with mapping; populated by usage.
- Bulk via `opensearchutil.BulkIndexer`, idempotent (`--recreate` drops first), deterministic seed flag.

### 1.11 Testing
Unit: builder golden tests (request JSON -> expected OpenSearch DSL JSON) per op/type; schema normalizer tests.
Integration: testcontainers OpenSearch 3.8 (security disabled) running the seeder with 2k docs; assert each op
returns expected hits. Contract: `oapi-codegen` types + response validation in tests.

---

## 2. query-ui (Next.js)

### 2.1 Stack
Next.js 16 app router, React 19, TypeScript, Tailwind v4, shadcn (radix, new-york, neutral), `@data-table-filters/*`
registry blocks, @tanstack/react-query 5, @tanstack/react-table 9, @tanstack/react-virtual, nuqs 2, zustand 5
(tabs + per-tab table state), `ai` 7 + `@ai-sdk/react` + `@ai-sdk/anthropic`, `ai-elements`, zod 4, recharts 3
(pulled by the chart block), cmdk, lucide, sonner, date-fns, superjson (dtf transport default; we'll set
`transport` to plain JSON since the Go API emits JSON).

Bootstrap. Start from the **infinite** example block, not the default table, so we get the virtualised
`DataTableInfinite`, bidirectional cursor fetching, timeline chart, live mode, sheet details and the
`examples/infinite` schema/query-options/api files to adapt:
```
pnpm dlx shadcn@latest init @data-table-filters/data-table-example-infinite --name query-ui --template next -b radix
cd query-ui
npx shadcn@latest add sidebar tabs resizable sheet command dialog popover tooltip badge button input select \
  separator scroll-area skeleton dropdown-menu toggle-group calendar form textarea card table sonner
npx shadcn@latest add @data-table-filters/data-table-zustand   # example ships nuqs; we add zustand for per-tab stores
npx ai-elements@latest add conversation message prompt-input response reasoning tool suggestion shimmer
```
The example lands in `src/components/data-table/**`, `src/lib/**` and an `infinite` route with its own Next API
route and fake data; we delete that API route and point the transport at our BFF proxy instead. We own the copies.

### 2.2 Layout
```
src/
├── app/
│   ├── layout.tsx                 # Providers: QueryClient, NuqsAdapter, TooltipProvider, SidebarProvider, Theme
│   ├── page.tsx                   # Shell: AppSidebar | TabBar + active tab content | AI panel
│   └── api/
│       ├── chat/route.ts          # AI SDK streamText, tools: propose_query (placeholder)
│       └── proxy/[...path]/route.ts  # BFF -> query-api; forwards Authorization header if present (none locally)
├── components/
│   ├── shell/app-sidebar.tsx      # shadcn Sidebar collapsible="icon" (labels via Tooltip on hover)
│   ├── shell/tab-bar.tsx          # top tabs: landing + N query tabs, close/rename/reorder (dnd-kit), "+"
│   ├── shell/ai-panel.tsx         # right ResizablePanel (collapsible) holding AI Elements Conversation
│   ├── landing/recent-queries.tsx # GET /queries -> cards/table; click -> opens tab
│   ├── landing/new-query.tsx      # pick model(s) (default all) -> "Start with builder" | "Start with search"
│   ├── query/query-tab.tsx        # one tab = one QueryWorkspace (indices[] + store + table)
│   ├── query/query-builder.tsx    # schema-driven rows: field -> op (from schema.ops) -> typed value input
│   ├── query/lucene-bar.tsx       # cmdk-based Lucene input: field/operator/value completions + validate
│   ├── query/semantic-input.tsx   # "Ask semantically" input -> semantic clause
│   ├── data-table/**              # from @data-table-filters (owned copies)
│   └── ai-elements/**             # from ai-elements
├── lib/
│   ├── api/client.ts              # generated from query-api/api/openapi.yaml (openapi-typescript + fetch)
│   ├── api/query-options.ts       # TanStack Query options: schema, values, queries, infinite query
│   ├── schema/to-table-schema.ts  # API schema -> createTableSchema()/col.* (dynamic columns + filterFields)
│   ├── schema/filter-mapping.ts   # dtf filter state (checkbox/slider/timerange/input) <-> API filters[]
│   ├── store/tabs.ts              # zustand persisted: tabs[{id,type,index,title,state}], activeTabId
│   └── lucene/                    # tokenizer + completion engine for the Lucene bar
└── hooks/
```

### 2.3 Key design points
- **Sidebar**: shadcn `Sidebar` with `collapsible="icon"`, `SidebarMenuButton tooltip=...` gives icon-only with
  hover labels out of the box. Items: Home, Models, Recent queries, Saved, Settings; footer user menu.
- **Tabs**: zustand store persisted to localStorage. Tab types: `landing`, `query`. Each query tab owns a
  data-table-filters **zustand adapter** store instance (block `data-table-zustand`) so inactive tabs keep filter,
  sort, column visibility, scroll cursor. The active tab is mirrored into the URL through nuqs (`?tab=<id>` plus
  dtf's serializer) so a tab can be shared/reopened; on load, URL wins over storage for that tab.
- **Multi-model result sets**: a tab can search any subset of models (default all). The API returns rows from
  several indices in one sorted stream (`search_after` sort with `_id` tiebreaker across indices). UI handling:
  - Columns are generated from the **merged schema** for the selected models; a field missing on a row renders
    as an empty muted cell, never an error. Default visible columns = fields common to all selected models plus
    the tab's time field; per-model-only fields start hidden but are available in view options and filters.
  - A pinned `_model` column (badge cell) is shown whenever more than one model is selected, with a checkbox
    facet in the filter rail so the user can narrow to one model without leaving the tab.
  - Filter fields marked `conflict` in the schema render disabled with a tooltip until the model set is narrowed.
  - Filter controls, command-bar completions and the query builder read from the merged schema; `/values` and
    `/autocomplete` are always called with the tab's `indices` so options reflect only those models.
  - Row detail sheet groups fields by model definition and shows the source index name.
  - The histogram uses the merged time field; the series selector offers `_model` so the chart can be stacked
    by model, plus any enum common to the selected models.
- **Table per tab**: `DataTableInfinite` + `TimelineChart` in `chartSlot` (kept at top; drag selects time range),
  `DataTableFilterControls` on the left rail, a model (`_model`) column + facet when more than one model is
  searched, `DataTableFilterCommand` in the toolbar, `DataTableSheetDetails`
  for row detail. Data via `useInfiniteQuery(createDataTableQueryOptions(...))` with `transport` pointed at
  `/api/proxy/v1/search/query` (with `indices` from the tab); the serializer converts dtf URL state to our POST body
  (`filter-mapping.ts`). `_meta=false` -> `meta:false` on pagination pages.
- **Dynamic schema -> table**: `to-table-schema.ts` maps API field types to `col.*` factories: enum/keyword ->
  `col.enum().filterable("checkbox")` (options from schema enum or lazily from `/values`), number ->
  `col.number().filterable("slider")` (min/max from facets), date -> `col.date().filterable("timerange")`,
  text -> `col.text().filterable("input")`, boolean -> checkbox true/false, ip/mac -> `input` with validation
  and a `cidr`/`prefix` op toggle, geo -> custom filter component (lat/lon/radius) added to dtf's filter union
  (`type: "geo"`), vector -> hidden. Cell renderers picked from `x-ui.cell` or type defaults
  (timestamp, badge, status-code, level-indicator, boolean, number, code).
- **Starting a query**: landing "New query" -> pick model(s), default all -> tab opens with either the builder (rows of
  field/op/value, uses `/values` for option dropdowns and autocomplete) or focus in the command bar. Builder state
  and dtf filter state are two views of the same `filters[]`; changing either updates the other.
- **Lucene bar**: separate mode toggle in the toolbar. cmdk-based suggestions: field names + descriptions from the
  schema, operators (`AND OR NOT TO`, ranges `[a TO b]`, wildcards), values from `/autocomplete` when caret is
  after `field:`. Debounced `/validate` shows syntax errors inline. dtf's own `field:value` command bar stays for
  structured filters (it already has field/value autocomplete).
- **Recent queries**: landing tab lists `/queries` (mine), shows index, summary of filters, result count, time;
  "Open" recreates the tab state from the saved request.
- **AI panel**: `ResizablePanelGroup` (table | panel), panel collapsible to an icon rail; below `md` it becomes a
  `Sheet`. AI Elements `Conversation`, `Message`, `PromptInput`, `Suggestion`, `Tool`. `useChat` ->
  `/api/chat`, which runs `streamText` with `@ai-sdk/anthropic` (`claude-sonnet-5`) and a `propose_query` tool
  whose schema is our QueryRequest; the tool result renders a "Run in new tab" card. Prompt includes the active
  index schema. Placeholder-level for now: wiring + one tool, no eval loop.
- **Auth**: none locally. The BFF proxy forwards an `Authorization` header when the browser sends one, so a real
  IdP can be added later without touching components.

---

## 3. Milestones
1. **Infra + seed** (day 1): compose up (security off), `cmd/seed` with `web_logs`/`devices`/`queries`, schemas dir.
2. **API core** (days 2-4): schema registry + `/schema`, `/query` with all filter ops, cursor, histogram, facets,
   request logging, Authorization passthrough, audit writes, `/queries`, `/aggregate`. Golden + integration tests.
3. **API extras** (day 5): `/values`, `/autocomplete`, `/validate`, semantic (`fake` + `ollama` embedders).
4. **UI shell** (day 6): init from `data-table-example-infinite`,  sidebar (icon mode), tab bar, landing, AI panel placeholder, BFF proxy.
5. **UI table** (days 7-9): dynamic merged schema -> table, multi-model rows (`_model` column/facet, sparse cells), infinite query, chart, filter rail,
   command bar, sheet details, per-tab zustand stores + URL mirror.
6. **UI query entry** (days 10-11): query builder, Lucene bar with completions + validation, semantic input,
   recent queries -> reopen.
7. **AI basics** (day 12): `/api/chat` + `propose_query` tool + "run in tab".
8. **Polish**: generated TS client from OpenAPI, Docker builds, README per repo, CI (go test, vitest, lint).

## 4. Open questions (defaults chosen; say so if you want otherwise)
- Index prefix: `OS_INDEX_PREFIX` env, `ds_` in local `.env`; model `web_logs` -> `ds_web_logs`.
- Exact counts: `track_total_hits: true` by default. Switch to a threshold if indices grow past ~50M docs.
- Cross-model field type conflicts: rejected with an error unless `indices` narrows the set. Alternative is to
  silently apply the filter only to compatible models; say if you prefer that.
- Embedding provider default: `ollama` locally with `nomic-embed-text` (768 dims); `fake` in tests. Confirm which
  hosted provider you actually want in prod so the seeder uses matching dims.
- shadcn library: Radix (default) vs Base UI. dtf supports both; plan assumes Radix.
- Nested/object fields: plan flattens dotted paths and wraps `nested` mappings; confirm your Pydantic models use nested.
- Saved queries: audit-only for now, or also user-named "saved/pinned" queries (adds a `saved` flag + PUT)?
