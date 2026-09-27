package query

import (
	"context"
	"strings"

	"github.com/prismgroup/query-api/internal/embed"
	"github.com/prismgroup/query-api/internal/schema"
)

// Builder turns Requests into OpenSearch search bodies.
type Builder struct {
	Registry       *schema.Registry
	Embedder       embed.Embedder
	TrackTotalHits any // true | false | int
	MaxSize        int
}

// Plan is a resolved request ready to send.
type Plan struct {
	Models    []*schema.Model
	Indices   []string
	Fields    []*schema.MergedField
	Body      M
	Sort      []Sort
	TimeField string
	Semantic  bool
}

// Resolve validates the request against the schema and produces the search body.
// withHits=false produces an aggregation-only body (size 0).
func (b *Builder) Resolve(ctx context.Context, r *Request, withHits bool) (*Plan, error) {
	models, indices, err := b.Registry.Resolve(r.Indices)
	if err != nil {
		return nil, &BadRequest{Msg: err.Error()}
	}
	fields := schema.Merge(models)
	p := &Plan{Models: models, Indices: indices, Fields: fields, TimeField: timeField(models)}

	q, err := b.query(ctx, r, p)
	if err != nil {
		return nil, err
	}
	body := M{"query": q}

	if withHits {
		size := r.Size
		if size <= 0 {
			size = 50
		}
		if b.MaxSize > 0 && size > b.MaxSize {
			size = b.MaxSize
		}
		body["size"] = size
		sort, osSort, err := b.sort(r, p)
		if err != nil {
			return nil, err
		}
		p.Sort = sort
		body["sort"] = osSort
		if r.Cursor != "" {
			after, err := decodeCursor(r.Cursor)
			if err != nil {
				return nil, err
			}
			body["search_after"] = after
		}
		if len(r.Fields) > 0 {
			body["_source"] = r.Fields
		} else {
			// vectors are large and useless to the UI
			var excl []string
			for _, f := range fields {
				if f.Type == schema.TypeVector {
					excl = append(excl, f.Name)
				}
			}
			if len(excl) > 0 {
				body["_source"] = M{"excludes": excl}
			}
		}
		body["track_total_hits"] = false
		if r.WantMeta() {
			body["track_total_hits"] = b.TrackTotalHits
		}
	} else {
		body["size"] = 0
		body["track_total_hits"] = b.TrackTotalHits
	}

	if r.WantMeta() || !withHits {
		aggs, err := b.aggs(r, p)
		if err != nil {
			return nil, err
		}
		if len(aggs) > 0 {
			body["aggs"] = aggs
		}
	}
	p.Body = body
	return p, nil
}

// query assembles the bool query from lucene, text, semantic and filters.
func (b *Builder) query(ctx context.Context, r *Request, p *Plan) (M, error) {
	var must, filter, mustNot []M

	if s := strings.TrimSpace(r.Lucene); s != "" {
		must = append(must, luceneClause(s, p))
	}
	if s := strings.TrimSpace(r.Text); s != "" {
		var names []string
		for _, f := range p.Fields {
			if f.Searchable && !f.Conflict {
				names = append(names, f.Name)
			}
		}
		if len(names) == 0 {
			return nil, badf("no searchable text fields in the selected models")
		}
		must = append(must, M{"multi_match": M{"query": s, "fields": names, "type": "best_fields", "operator": "and", "lenient": true}})
	}
	for _, f := range r.Filters {
		fld := schema.Lookup(p.Fields, f.Field)
		if fld == nil {
			return nil, badf("unknown field %q", f.Field)
		}
		c, neg, err := filterClause(f, fld)
		if err != nil {
			return nil, err
		}
		if neg {
			mustNot = append(mustNot, c)
		} else {
			filter = append(filter, c)
		}
	}

	if r.Semantic != nil && strings.TrimSpace(r.Semantic.Text) != "" {
		knn, err := b.semanticClause(ctx, r.Semantic, p, filter, mustNot)
		if err != nil {
			return nil, err
		}
		p.Semantic = true
		// knn carries the filters itself for efficient pre-filtering; keep must clauses outside.
		bq := M{"must": append([]M{knn}, must...)}
		return M{"bool": bq}, nil
	}

	bq := M{}
	if len(must) > 0 {
		bq["must"] = must
	}
	if len(filter) > 0 {
		bq["filter"] = filter
	}
	if len(mustNot) > 0 {
		bq["must_not"] = mustNot
	}
	if len(bq) == 0 {
		return M{"match_all": M{}}, nil
	}
	return M{"bool": bq}, nil
}

func luceneClause(s string, p *Plan) M {
	var names []string
	for _, f := range p.Fields {
		if f.Searchable && !f.Conflict {
			names = append(names, f.Name)
		}
	}
	return M{"query_string": M{
		"query":            s,
		"fields":           names, // used for bare terms without a field
		"default_operator": "AND",
		"lenient":          true,
		"analyze_wildcard": true,
	}}
}

func (b *Builder) semanticClause(ctx context.Context, s *Semantic, p *Plan, filter, mustNot []M) (M, error) {
	var vec *schema.MergedField
	for _, f := range p.Fields {
		if f.Type == schema.TypeVector && !f.Conflict {
			vec = f
			break
		}
	}
	if vec == nil {
		return nil, badf("selected models have no vector field for semantic search")
	}
	if b.Embedder == nil {
		return nil, badf("semantic search is not configured")
	}
	if b.Embedder.Dims() != vec.Dimension {
		return nil, badf("embedder dims %d do not match field %q dims %d", b.Embedder.Dims(), vec.Name, vec.Dimension)
	}
	vecs, err := b.Embedder.Embed(ctx, []string{s.Text})
	if err != nil {
		return nil, err
	}
	k := s.K
	if k <= 0 {
		k = 100
	}
	knn := M{"vector": vecs[0], "k": k}
	if len(filter) > 0 || len(mustNot) > 0 {
		fb := M{}
		if len(filter) > 0 {
			fb["filter"] = filter
		}
		if len(mustNot) > 0 {
			fb["must_not"] = mustNot
		}
		knn["filter"] = M{"bool": fb}
	}
	return M{"knn": M{vec.Name: knn}}, nil
}

// sort validates the sort and appends a tiebreaker; semantic searches sort by score.
func (b *Builder) sort(r *Request, p *Plan) ([]Sort, []any, error) {
	if p.Semantic {
		return []Sort{{Field: "_score", Order: "desc"}}, []any{M{"_score": "desc"}, M{"_id": "asc"}}, nil
	}
	sorts := r.Sort
	if len(sorts) == 0 && p.TimeField != "" {
		sorts = []Sort{{Field: p.TimeField, Order: "desc"}}
	}
	var out []any
	var norm []Sort
	for _, s := range sorts {
		order := strings.ToLower(s.Order)
		if order == "" {
			order = "asc"
		}
		if order != "asc" && order != "desc" {
			return nil, nil, badf("sort order must be asc or desc")
		}
		if r.Direction == "prev" {
			order = flip(order)
		}
		if s.Field == "_score" {
			out = append(out, M{"_score": order})
			norm = append(norm, Sort{Field: "_score", Order: order})
			continue
		}
		fld := schema.Lookup(p.Fields, s.Field)
		if fld == nil {
			return nil, nil, badf("unknown sort field %q", s.Field)
		}
		if !fld.Sortable {
			return nil, nil, badf("field %q is not sortable", s.Field)
		}
		out = append(out, M{s.Field: M{"order": order, "unmapped_type": unmappedType(fld.Type), "missing": "_last"}})
		norm = append(norm, Sort{Field: s.Field, Order: order})
	}
	out = append(out, M{"_id": "asc"})
	return norm, out, nil
}

func flip(o string) string {
	if o == "asc" {
		return "desc"
	}
	return "asc"
}

func unmappedType(t schema.FieldType) string {
	switch t {
	case schema.TypeInteger:
		return "long"
	case schema.TypeFloat:
		return "double"
	case schema.TypeDate:
		return "date"
	case schema.TypeBoolean:
		return "boolean"
	case schema.TypeIP:
		return "ip"
	default:
		return "keyword"
	}
}

// timeField picks the shared time field across models, or "" if they disagree.
func timeField(models []*schema.Model) string {
	tf := ""
	for _, m := range models {
		if tf == "" {
			tf = m.TimeField
		} else if m.TimeField != tf {
			return ""
		}
	}
	return tf
}
