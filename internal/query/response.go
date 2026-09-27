package query

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/prismgroup/query-api/internal/schema"
)

// osResponse is the subset of the OpenSearch search response we read.
type osResponse struct {
	Took int64 `json:"took"`
	Hits struct {
		Total *struct {
			Value    int64  `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []struct {
			Index  string          `json:"_index"`
			ID     string          `json:"_id"`
			Score  *float64        `json:"_score"`
			Source json.RawMessage `json:"_source"`
			Sort   []any           `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}

// FacetRow is one value with its document count.
type FacetRow struct {
	Value any   `json:"value"`
	Total int64 `json:"total"`
}

// Facet is the distribution of one field within the current result set.
type Facet struct {
	Rows  []FacetRow `json:"rows,omitempty"`
	Total int64      `json:"total"`
	Min   *float64   `json:"min,omitempty"`
	Max   *float64   `json:"max,omitempty"`
}

// Meta accompanies the first page of results.
type Meta struct {
	TotalRowCount          int64            `json:"totalRowCount"`
	FilterRowCount         int64            `json:"filterRowCount"`
	FilterRowCountRelation string           `json:"filterRowCountRelation"`
	TookMs                 int64            `json:"tookMs"`
	ChartData              []map[string]any `json:"chartData,omitempty"`
	ChartSeries            []string         `json:"chartSeries,omitempty"`
	Facets                 map[string]Facet `json:"facets,omitempty"`
	ModelCounts            map[string]int64 `json:"modelCounts,omitempty"`
	TimeField              string           `json:"timeField,omitempty"`
	QueryID                string           `json:"queryId,omitempty"`
}

// Response is the body of POST /v1/search/query.
type Response struct {
	Data       []map[string]any `json:"data"`
	Meta       *Meta            `json:"meta,omitempty"`
	NextCursor *string          `json:"nextCursor"`
	PrevCursor *string          `json:"prevCursor"`
}

// Decode converts the raw OpenSearch response into the API response.
func (b *Builder) Decode(p *Plan, r *Request, raw json.RawMessage, totalRowCount int64) (*Response, error) {
	var osr osResponse
	if err := json.Unmarshal(raw, &osr); err != nil {
		return nil, err
	}
	out := &Response{Data: make([]map[string]any, 0, len(osr.Hits.Hits))}
	hits := osr.Hits.Hits
	if r.Direction == "prev" {
		for i, j := 0, len(hits)-1; i < j; i, j = i+1, j-1 {
			hits[i], hits[j] = hits[j], hits[i]
		}
	}
	for _, h := range hits {
		row := map[string]any{}
		if len(h.Source) > 0 {
			if err := json.Unmarshal(h.Source, &row); err != nil {
				return nil, err
			}
		}
		row["_id"] = h.ID
		row["_index"] = h.Index
		row["_model"] = b.Registry.ModelName(h.Index)
		if h.Score != nil && p.Semantic {
			row["_score"] = *h.Score
		}
		out.Data = append(out.Data, row)
	}
	if n := len(hits); n > 0 {
		size := r.Size
		if size <= 0 {
			size = 50
		}
		// Rows are in display order here, so the first row bounds "prev" and the last bounds "next".
		out.PrevCursor = strPtr(encodeCursor(hits[0].Sort))
		if n >= size || r.Direction == "prev" {
			out.NextCursor = strPtr(encodeCursor(hits[n-1].Sort))
		}
	}
	if r.WantMeta() {
		out.Meta = b.decodeMeta(p, r, &osr, totalRowCount)
	}
	return out, nil
}

// DecodeMeta decodes an aggregation-only response.
func (b *Builder) DecodeMeta(p *Plan, r *Request, raw json.RawMessage, totalRowCount int64) (*Meta, error) {
	var osr osResponse
	if err := json.Unmarshal(raw, &osr); err != nil {
		return nil, err
	}
	return b.decodeMeta(p, r, &osr, totalRowCount), nil
}

func (b *Builder) decodeMeta(p *Plan, r *Request, osr *osResponse, totalRowCount int64) *Meta {
	m := &Meta{TookMs: osr.Took, TotalRowCount: totalRowCount, TimeField: p.TimeField, FilterRowCountRelation: "eq"}
	if osr.Hits.Total != nil {
		m.FilterRowCount = osr.Hits.Total.Value
		m.FilterRowCountRelation = osr.Hits.Total.Relation
	}
	if raw, ok := osr.Aggregations[aggModels]; ok {
		var t termsAgg
		_ = json.Unmarshal(raw, &t)
		m.ModelCounts = map[string]int64{}
		for _, bk := range t.Buckets {
			m.ModelCounts[b.Registry.ModelName(bk.KeyString())] = bk.DocCount
		}
	}
	if raw, ok := osr.Aggregations[aggHistogram]; ok {
		m.ChartData, m.ChartSeries = decodeHistogram(raw, b.Registry, r.Histogram != nil && r.Histogram.Series == "_model")
	}
	for _, name := range r.Facets {
		if m.Facets == nil {
			m.Facets = map[string]Facet{}
		}
		if raw, ok := osr.Aggregations[aggFacet+name]; ok {
			var t termsAgg
			_ = json.Unmarshal(raw, &t)
			f := Facet{}
			for _, bk := range t.Buckets {
				f.Rows = append(f.Rows, FacetRow{Value: bk.Key, Total: bk.DocCount})
				f.Total += bk.DocCount
			}
			f.Total += t.SumOther
			m.Facets[name] = f
		}
		if raw, ok := osr.Aggregations[aggStats+name]; ok {
			var s struct {
				Count int64    `json:"count"`
				Min   *float64 `json:"min"`
				Max   *float64 `json:"max"`
			}
			_ = json.Unmarshal(raw, &s)
			m.Facets[name] = Facet{Total: s.Count, Min: s.Min, Max: s.Max}
		}
	}
	return m
}

type bucket struct {
	Key         any       `json:"key"`
	KeyAsString string    `json:"key_as_string"`
	DocCount    int64     `json:"doc_count"`
	Series      *termsAgg `json:"series"`
}

func (bk bucket) KeyString() string {
	if s, ok := bk.Key.(string); ok {
		return s
	}
	if bk.KeyAsString != "" {
		return bk.KeyAsString
	}
	if f, ok := bk.Key.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

type termsAgg struct {
	Buckets  []bucket `json:"buckets"`
	SumOther int64    `json:"sum_other_doc_count"`
}

// decodeHistogram flattens date buckets into rows keyed by series value, which
// is the shape the timeline chart consumes: {timestamp, <series>: count, ...}.
func decodeHistogram(raw json.RawMessage, reg *schema.Registry, modelSeries bool) ([]map[string]any, []string) {
	var t termsAgg
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, nil
	}
	seriesSet := map[string]bool{}
	rows := make([]map[string]any, 0, len(t.Buckets))
	for _, bk := range t.Buckets {
		ts, _ := bk.Key.(float64)
		row := map[string]any{"timestamp": int64(ts), "total": bk.DocCount}
		if bk.Series != nil {
			for _, sb := range bk.Series.Buckets {
				k := sb.KeyString()
				if modelSeries {
					k = reg.ModelName(k)
				}
				row[k] = sb.DocCount
				seriesSet[k] = true
			}
		}
		rows = append(rows, row)
	}
	series := make([]string, 0, len(seriesSet))
	for k := range seriesSet {
		series = append(series, k)
	}
	sort.Strings(series)
	if len(series) == 0 {
		series = []string{"total"}
	}
	return rows, series
}

// ValuesResponse is the body of /values and /autocomplete.
type ValuesResponse struct {
	Field  string     `json:"field"`
	Values []FacetRow `json:"values"`
	After  *string    `json:"after"`
}

// DecodeValues reads either a terms or composite "values" aggregation.
func DecodeValues(field string, raw json.RawMessage, size int) (*ValuesResponse, error) {
	var osr osResponse
	if err := json.Unmarshal(raw, &osr); err != nil {
		return nil, err
	}
	out := &ValuesResponse{Field: field, Values: []FacetRow{}}
	var agg struct {
		Buckets []struct {
			Key      any   `json:"key"`
			DocCount int64 `json:"doc_count"`
		} `json:"buckets"`
		AfterKey map[string]any `json:"after_key"`
	}
	if err := json.Unmarshal(osr.Aggregations["values"], &agg); err != nil {
		return nil, err
	}
	for _, bk := range agg.Buckets {
		v := bk.Key
		if m, ok := v.(map[string]any); ok { // composite key {"v": ...}
			v = m["v"]
		}
		out.Values = append(out.Values, FacetRow{Value: v, Total: bk.DocCount})
	}
	if agg.AfterKey != nil {
		out.After = strPtr(encodeCursor([]any{agg.AfterKey["v"]}))
	}
	return out, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
