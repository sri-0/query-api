package query

import (
	"github.com/prismgroup/query-api/internal/schema"
)

const (
	aggHistogram = "histogram"
	aggFacet     = "facet_"
	aggStats     = "stats_"
	aggModels    = "models"
)

// aggs builds histogram, facet and model-count aggregations.
func (b *Builder) aggs(r *Request, p *Plan) (M, error) {
	out := M{}
	if len(p.Models) > 1 {
		out[aggModels] = M{"terms": M{"field": "_index", "size": len(p.Models)}}
	}
	if h := r.Histogram; h != nil {
		field := h.Field
		if field == "" {
			field = p.TimeField
		}
		if field == "" {
			return nil, badf("histogram.field is required when the selected models have different time fields")
		}
		fld := schema.Lookup(p.Fields, field)
		if fld == nil || fld.Type != schema.TypeDate || fld.Conflict {
			return nil, badf("histogram field %q must be a date field", field)
		}
		dh := M{"field": field, "min_doc_count": 0}
		if h.Interval == "" || h.Interval == "auto" {
			out[aggHistogram] = M{"auto_date_histogram": M{"field": field, "buckets": 60}}
		} else {
			dh["fixed_interval"] = h.Interval
			out[aggHistogram] = M{"date_histogram": dh}
		}
		if h.Series != "" {
			sub := M{}
			if h.Series == "_model" {
				sub["series"] = M{"terms": M{"field": "_index", "size": 20}}
			} else {
				sf := schema.Lookup(p.Fields, h.Series)
				if sf == nil || !sf.Aggregatable || sf.Conflict {
					return nil, badf("histogram series field %q must be aggregatable", h.Series)
				}
				sub["series"] = M{"terms": M{"field": h.Series, "size": 20, "missing": "(none)"}}
			}
			out[aggHistogram].(M)["aggs"] = sub
		}
	}
	for _, name := range r.Facets {
		fld := schema.Lookup(p.Fields, name)
		if fld == nil {
			return nil, badf("unknown facet field %q", name)
		}
		if fld.Conflict || !fld.Aggregatable {
			return nil, badf("facet field %q is not aggregatable", name)
		}
		switch fld.Type {
		case schema.TypeInteger, schema.TypeFloat, schema.TypeDate:
			out[aggStats+name] = M{"stats": M{"field": name}}
		default:
			out[aggFacet+name] = M{"terms": M{"field": name, "size": 50}}
		}
	}
	return out, nil
}

// ValuesBody builds an aggregation-only body listing distinct values of one field.
func (b *Builder) ValuesBody(p *Plan, fld *schema.MergedField, req *ValuesRequest, autocomplete bool) (M, error) {
	size := req.Size
	if size <= 0 || size > 500 {
		size = 100
	}
	terms := M{"field": fld.Name, "size": size, "order": []M{{"_count": "desc"}, {"_key": "asc"}}}
	switch {
	case autocomplete && req.Query != "":
		// case-insensitive contains; regexp include is only valid for string-keyed terms
		if fld.Type == schema.TypeKeyword || fld.Type == schema.TypeMAC {
			terms["include"] = ".*" + caseInsensitiveRegex(req.Query) + ".*"
		}
	case req.Prefix != "":
		if fld.Type == schema.TypeKeyword || fld.Type == schema.TypeMAC {
			terms["include"] = regexQuote(req.Prefix) + ".*"
		}
	}
	if req.After != "" {
		// keyset paging with a composite aggregation
		after, err := decodeCursor(req.After)
		if err != nil {
			return nil, err
		}
		comp := M{"size": size, "sources": []M{{"v": M{"terms": M{"field": fld.Name}}}}, "after": M{"v": after[0]}}
		return M{"size": 0, "query": p.Body["query"], "aggs": M{"values": M{"composite": comp}}}, nil
	}
	return M{"size": 0, "query": p.Body["query"], "aggs": M{"values": M{"terms": terms}}}, nil
}

func regexQuote(s string) string {
	const special = `.?+*|{}[]()"\#@&<>~`
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		if indexByte(special, s[i]) {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func caseInsensitiveRegex(s string) string {
	out := make([]byte, 0, len(s)*4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, '[', c, c-32, ']')
		case c >= 'A' && c <= 'Z':
			out = append(out, '[', c+32, c, ']')
		default:
			out = append(out, []byte(regexQuote(string(c)))...)
		}
	}
	return string(out)
}

func indexByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}
