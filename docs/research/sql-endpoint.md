# A SQL endpoint for query-api: options for SQL and joins over OpenSearch

Research date: 2026-10-06. Combines the OpenSearch SQL/PPL plugin docs, Go library research, and
measurements against the local OpenSearch 3.8.0 cluster (`ds_web_logs` 200k rows, `ds_alerts` 20k,
`ds_devices` 2k). Measured results are marked **measured**; everything else cites a source.

## Verdict

1. **Expose SQL by passing it through to `_plugins/_sql` after validating it in Go.** Single-index SQL
   (WHERE, GROUP BY, ORDER BY, window functions, date functions, relevance functions, cursor paging) works
   well and runs entirely inside OpenSearch. This is cheap to build: parse with Vitess `sqlparser`, allow only
   `SELECT`, allowlist `ds_*` tables, inject a LIMIT, forward the user's Authorization header, log it.
2. **Do not rely on SQL `JOIN` in the plugin.** On 3.8 it still executes on the legacy engine: two indices
   only, hash join over at most 200 rows per side unless a LIMIT is given, and `GROUP BY` or `COUNT(*)`
   over the joined result is wrong or errors (**measured**, below).
3. **PPL `join` is the working join engine today.** It runs on the Calcite engine, supports inner/left
   (right/full/cross behind a setting), subsearches, `stats` after the join, `cidrmatch` and `match`, and
   returned correct aggregates over the full data set (**measured**). The right side is capped at 50,000
   rows by `plugins.ppl.join.subsearch_maxout` (configurable, 0 = unlimited). Options: offer PPL as a second
   query language in the UI, or translate the join subset of SQL into PPL in the API.
4. **For unrestricted SQL with joins, run the join in the API with embedded DuckDB** (official
   `github.com/duckdb/duckdb-go`, CGO): parse SQL in Go, pull each referenced index with pushed-down filters
   and projected columns via `search_after`, load into an in-memory DuckDB, execute the user's SQL there.
   Caps on rows, memory and time make it safe. Pure-Go alternative: `go-mysql-server` with a virtual table
   over the OpenSearch stream. A Trino sidecar is the most capable but adds a JVM.

Recommended sequence: phase 1 = SQL pass-through plus PPL pass-through (days); phase 2 = DuckDB
federation behind the same endpoint when the SQL contains a join or an aggregation over a join (a week or
two). Details follow.

## What the plugin does on 3.8 (measured)

| Query | Result |
|---|---|
| `SELECT host, level, COUNT(*) ... GROUP BY ... ORDER BY ... LIMIT 3` | Correct, `jdbc` format with schema/datarows |
| `SELECT ... FROM ds_web_logs w JOIN ds_alerts a ON w.host = a.host WHERE ... LIMIT 5` | Rows returned; `_explain` shows legacy `BlockHashJoin`, each side fetched with `size: 200` |
| `SELECT COUNT(*) FROM w JOIN a ...` | Wrong: 200 rows of nulls (legacy join cannot aggregate) |
| `SELECT w.host, COUNT(*) FROM w JOIN a ... GROUP BY w.host` | Error `table alias or field name missing` |
| `LEFT JOIN` two indices | Runs (legacy) |
| `WHERE host IN (SELECT host FROM ds_alerts ...)` | Works (rewritten to a hash join) |
| `MATCH(message, 'timeout')`, `QUERY_STRING(['message'], ...)` | Work |
| `CIDRMATCH(src_ip, '10.0.0.0/8')` in SQL | `unsupported method: CIDRMATCH` |
| `ROW_NUMBER() OVER (PARTITION BY host ORDER BY timestamp DESC)` | Works |
| `DATE_FORMAT(timestamp, '%Y-%m-%d')` + GROUP BY | Works |
| `fetch_size: 2` | Returns a `cursor`; `_plugins/_sql/close` releases it |
| `DELETE FROM ...` | Rejected: `plugins.sql.delete.enabled` is false by default |
| PPL `source=ds_alerts \| where severity='critical' \| join left=a right=w ON a.host=w.host ds_web_logs \| stats count() by a.host` | Correct counts (407,424 for web-01); Calcite plan with `LogicalJoin` and `JOIN_SUBSEARCH_MAXOUT 50000` |
| PPL `where cidrmatch(src_ip,'10.0.0.0/8') and match(message,'timeout')` | Works |
| PPL `join type=left ... \| stats count() by d.vendor` | Works |
| PPL `where host in [ source=ds_alerts \| ... \| fields host ]` (subsearch) | Works |
| PPL with `fetch_size` | No cursor (cap only) |
| PPL `eval rn = row_number() over (...)` | Syntax error (not supported in PPL) |

Cluster defaults seen: `plugins.calcite.enabled=true`, `plugins.calcite.fallback.allowed=false`,
`plugins.calcite.all_join_types.allowed=false`, `plugins.query.size_limit=10000`,
`plugins.ppl.join.subsearch_maxout=50000`, `plugins.ppl.subsearch.maxout=10000`,
`plugins.ppl.query.timeout=300s`, `plugins.query.memory_limit` 85%.

Note on versions: the 3.7.0 release notes describe a "unified SQL path" routing SQL through Calcite with
JOIN, IN/EXISTS subqueries, derived tables and window functions
([release notes](https://github.com/opensearch-project/opensearch-build/blob/main/release-notes/opensearch-release-notes-3.7.0.md)),
but the user docs still document SQL JOIN as legacy-only
([complex queries](https://docs.opensearch.org/latest/sql-and-ppl/sql/complex/),
[limitations](https://docs.opensearch.org/latest/sql-and-ppl/limitation/)) and our 3.8 cluster behaved as
legacy. Treat SQL JOIN in the plugin as unreliable until a release where `_explain` shows a Calcite plan.

## Plugin facts that shape the design

- Endpoints: `POST _plugins/_sql`, `_sql/_explain`, `_sql/close`, `_plugins/_ppl`, `_ppl/_explain`,
  `PUT _plugins/_query/settings`. Body: `query`, optional `filter` (raw DSL merged in), `fetch_size`
  (SQL, jdbc format only), `cursor`, `parameters` for `?` placeholders. Formats `jdbc` (default), `csv`,
  `raw`; `json` is legacy-only ([API](https://docs.opensearch.org/latest/sql-and-ppl/sql-and-ppl-api/index/),
  [formats](https://docs.opensearch.org/latest/sql-and-ppl/response-formats/)).
- Engines: V1 legacy, V2, V3 Calcite (default on since 3.3; fallback to V2 disabled by default since 3.2).
  V3 is PPL-first; SQL is forwarded to V2/V1 for unsupported shapes
  ([engine intro](https://github.com/opensearch-project/sql/blob/main/docs/dev/intro-v3-engine.md)).
- Limits: `plugins.query.size_limit` 10,000 rows (also caps `max_result_window`); unpushed sorts and
  limits are computed over at most that many rows, so results can truncate silently
  ([basics](https://github.com/opensearch-project/sql/blob/main/docs/user/dql/basics.rst)). Legacy join:
  60 s timeout, `/*! JOIN_TIME_OUT(n) */` hint, both sides' WHERE push down.
- Types: `ip` becomes VARCHAR in SQL (no CIDR function in SQL; PPL has `cidrmatch`); `text` fields are
  auto-resolved to `.keyword` for term/sort/group; `geo_point` is struct-like
  ([datatypes](https://github.com/opensearch-project/sql/blob/main/docs/user/general/datatypes.rst)).
- Security: SQL/PPL execute as ordinary search actions, so index permissions, DLS and FLS apply when the
  caller's token is forwarded. Required permissions: `cluster:admin/opensearch/sql` (or `/ppl`),
  `indices:data/read/search*`, `indices:admin/mappings/get`
  ([security](https://github.com/opensearch-project/sql/blob/main/docs/user/ppl/admin/security.md)).
- Go client: `github.com/opensearch-project/opensearch-go/v5/plugins/sql` wraps `_plugins/_sql` with typed
  `QueryResp{Schema, Datarows, Total, Size, Status, Cursor}`, explain and close
  ([pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/plugins/sql)).

## Option A: validated pass-through (phase 1)

```
POST /v1/sql        { "sql": "...", "fetchSize": 1000, "cursor": "..." }
POST /v1/sql/explain
POST /v1/ppl        { "ppl": "source = ... | ..." }
```

Flow in Go:

1. Parse with `vitess.io/vitess/go/vt/sqlparser` (pure Go, MySQL dialect, active:
   [pkg.go.dev](https://pkg.go.dev/vitess.io/vitess/go/vt/sqlparser)). Accept only `*sqlparser.Select`
   (covers DML/DDL/SET/USE). `ExtractAllTables` must all match the model registry; rewrite model names to
   prefixed indices (`web_logs` to `ds_web_logs`) so users never see the prefix, which also acts as the
   allowlist. Reject wildcards and system indices.
2. Inject or clamp `LIMIT` (`Select.SetLimit`) to at most `plugins.query.size_limit`; reject `SELECT *` when
   the model has `text`/`vector` fields or expand `*` from the schema minus those fields.
3. Count joins: if the statement contains a `JOIN` or an aggregation over a join, either reject with a
   message pointing at PPL/phase 2, or route to the federated engine (Option C).
4. Forward via the opensearch-go `plugins/sql` client with the caller's Authorization header (our
   transport already does this), `format=jdbc`, request timeout from the Echo context.
5. Map the `jdbc` response to our table contract: `schema[]` gives column names and OpenSearch types, which
   `query-ui` can turn into a table schema the same way it does for `/v1/schema`. Return `cursor` for the
   infinite table's next page; call `_sql/close` when the tab closes.
6. Audit to `ds_queries` with `kind: "sql"` and the canonical SQL.

PPL pass-through is the same minus the parser: the plugin has no DML in PPL, so validation is "source
indices resolve to models" (regex on `source = ...` plus `join ... <index>` and `[ source = ... ]`), a `head`
clamp, and forwarding. PPL is where joins, `lookup`, `cidrmatch` and subsearches live
([join](https://docs.opensearch.org/latest/sql-and-ppl/ppl/commands/join/),
[lookup](https://docs.opensearch.org/latest/sql-and-ppl/ppl/commands/lookup/)).

Pros: no data movement, server-side circuit breaker, DLS/FLS for free, a day or two of work. Cons: joins
limited to PPL's shape and the 50k right-side cap; cursors only for simple SQL; silent truncation at
10,000 rows for unpushed sorts.

## Option B: translate SQL joins to PPL

Vitess gives the AST; a two-table inner/left join with equi-conditions, per-side filters, projection and an
optional GROUP BY maps cleanly onto `source = L | where ... | join type=... left=l right=r ON ... [ source
= R | where ... ] | stats ... by ... | fields ... | head n`. This keeps everything server-side but only for
the subset PPL supports (no window functions, no right-side beyond 50k rows unless the setting is raised,
no three-way joins without nesting). Worth doing only if phase 2 is rejected.

## Option C: federated execution in the API with DuckDB (phase 2)

```
SQL -> Vitess AST -> per-table: single-table WHERE conjuncts -> OpenSearch bool.filter
                                 projected columns          -> _source includes
                     PIT + search_after (size 5000) -> DuckDB appender (:memory: per request)
     -> run the original SQL in DuckDB -> stream rows back in the jdbc-like shape
```

- Engine: `github.com/duckdb/duckdb-go` (official since v2.5.0; the older `marcboeker/go-duckdb` was
  archived 2025-10-20). CGO with prebuilt static libraries; appender API; `memory_limit` and `threads` per
  connection ([repo](https://github.com/duckdb/duckdb-go)). Hundreds of thousands of rows with joins and
  aggregates is comfortable; binary grows by tens of MB. Pure-Go fallback: `github.com/dolthub/go-mysql-server`
  with a `sql.Table` whose `PartitionRows` streams `search_after` pages and `FilteredTable`/`ProjectedTable`
  push filters down ([backend guide](https://github.com/dolthub/go-mysql-server/blob/main/BACKEND.md)).
- Pushdown: translate each table's own conjuncts (`=`, `IN`, `<`, `>`, `BETWEEN`, `IS NULL`, `LIKE 'x%'`)
  to term/terms/range/exists/prefix on keyword and numeric fields; `text` only via `.keyword`; everything
  else stays in DuckDB. Reuse `internal/query/filters.go` for the translation.
- Type mapping: keyword/text VARCHAR, long BIGINT, double DOUBLE, boolean BOOLEAN, date TIMESTAMPTZ,
  ip VARCHAR (or DuckDB `inet`), geo_point STRUCT(lat, lon), arrays LIST, object/nested JSON.
- Guard rails: rows per index cap (e.g. 200k) and total cap, abort the scan when exceeded; one `:memory:`
  database per request with `memory_limit`; `context.WithTimeout` propagated to OpenSearch (`timeout`,
  `terminate_after`) and `db.QueryContext`; at most three indices per statement; the same Authorization
  forwarding so DLS/FLS still govern what is fetched.
- Pros: full SQL (joins of any arity, aggregates over joins, window functions, CTEs) with predictable
  semantics. Cons: data leaves the cluster into API memory; CGO build; latency proportional to rows fetched.

## Option D: Trino sidecar

Trino's OpenSearch connector gives full SQL with pushdown of predicates on numeric/keyword/date/boolean
fields, `raw_query()` for DSL, and a Go client (`github.com/trinodb/trino-go-client`)
([connector](https://trino.io/docs/current/connector/opensearch.html)). Most capable, but a JVM sidecar,
connector-level rather than per-user auth without extra work, and arrays need `_meta` hints. Reasonable if
Trino is already in the estate (it is also the Sleeper connector path from the lakehouse research).

## Prior art

Elastic SQL has no joins and flattenable subqueries only
([limitations](https://www.elastic.co/docs/explore-analyze/query-filter/languages/sql-limitations));
`github.com/cch123/elasticsql` converts SQL to DSL without joins (last push 2023); Grafana's OpenSearch
datasource offers Lucene and PPL, not SQL. Nobody exposes general SQL joins over OpenSearch without an
external engine, which matches the plugin findings above.

## Proposed API shape

```jsonc
POST /v1/sql
{ "sql": "SELECT w.host, COUNT(*) n FROM web_logs w JOIN alerts a ON w.host = a.host WHERE a.severity = 'critical' GROUP BY w.host ORDER BY n DESC LIMIT 20",
  "engine": "auto",        // auto | opensearch | federated
  "fetchSize": 1000, "cursor": null }

200 { "schema": [{"name":"host","type":"keyword"},{"name":"n","type":"long"}],
      "rows": [["web-01", 407424], ...], "total": 12, "engine": "federated", "cursor": null,
      "explain": {...}, "queryId": "..." }
```

`engine: auto` picks the plugin for single-index statements and the federated engine when the statement
has a join or an aggregation over a join. The UI gets a fifth composer mode, "SQL", with the schema
response driving a plain table (no facets or histogram unless the statement has a time column).

## Next steps

1. Phase 1: `/v1/sql` and `/v1/ppl` pass-through with Vitess validation, model-name rewriting, LIMIT clamp,
   jdbc-to-table mapping, cursor close, audit. Add a "SQL" mode to the composer and a results tab that
   renders from `schema[]`.
2. Phase 2: `internal/sqlfed` with DuckDB, reusing the filter translator for pushdown; `engine: auto`.
3. Re-test SQL JOIN on each OpenSearch upgrade via `_explain`; when it shows a Calcite plan with correct
   aggregates, let `auto` prefer the plugin for two-table joins.
