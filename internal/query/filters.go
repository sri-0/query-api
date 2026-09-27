package query

import (
	"encoding/json"
	"net"
	"regexp"
	"strings"

	"github.com/prismgroup/query-api/internal/schema"
)

var macRe = regexp.MustCompile(`^([0-9a-fA-F]{2}[:-]){0,5}[0-9a-fA-F]{0,2}$`)

// M is a JSON object.
type M = map[string]any

// filterClause converts one Filter into an OpenSearch query clause plus a flag
// saying whether it must be negated (wrapped in must_not).
func filterClause(f Filter, fld *schema.MergedField) (clause M, negate bool, err error) {
	if fld.Conflict {
		return nil, false, badf("field %q has conflicting types across the selected models (%v); narrow indices", f.Field, fld.Types)
	}
	if !fld.Supports(f.Op) {
		return nil, false, badf("op %q not supported on field %q of type %s", f.Op, f.Field, fld.Type)
	}
	name := f.Field
	switch f.Op {
	case schema.OpExists:
		return M{"exists": M{"field": name}}, false, nil
	case schema.OpNotExists:
		return M{"exists": M{"field": name}}, true, nil

	case schema.OpEq, schema.OpNe:
		v, err := scalar(f, fld)
		if err != nil {
			return nil, false, err
		}
		return M{"term": M{name: v}}, f.Op == schema.OpNe, nil

	case schema.OpIn, schema.OpNotIn:
		vs, err := list(f, fld)
		if err != nil {
			return nil, false, err
		}
		return M{"terms": M{name: vs}}, f.Op == schema.OpNotIn, nil

	case schema.OpPrefix:
		s, err := str(f)
		if err != nil {
			return nil, false, err
		}
		if fld.Type == schema.TypeMAC {
			s = strings.ToLower(s)
			if !macRe.MatchString(s) {
				return nil, false, badf("field %q: %q is not a MAC prefix", name, s)
			}
		}
		return M{"prefix": M{name: M{"value": s, "case_insensitive": fld.Type == schema.TypeKeyword}}}, false, nil

	case schema.OpWildcard:
		s, err := str(f)
		if err != nil {
			return nil, false, err
		}
		if fld.Type == schema.TypeText {
			name += ".keyword"
		}
		return M{"wildcard": M{name: M{"value": s, "case_insensitive": true}}}, false, nil

	case schema.OpMatch:
		s, err := str(f)
		if err != nil {
			return nil, false, err
		}
		return M{"match": M{name: M{"query": s, "operator": "and"}}}, false, nil

	case schema.OpMatchPhrase:
		s, err := str(f)
		if err != nil {
			return nil, false, err
		}
		return M{"match_phrase": M{name: s}}, false, nil

	case schema.OpGt, schema.OpGte, schema.OpLt, schema.OpLte:
		v, err := scalar(f, fld)
		if err != nil {
			return nil, false, err
		}
		return M{"range": M{name: M{string(f.Op): v}}}, false, nil

	case schema.OpBetween:
		var pair []json.RawMessage
		if err := json.Unmarshal(f.Value, &pair); err != nil || len(pair) != 2 {
			return nil, false, badf("field %q: between needs a [from, to] pair", name)
		}
		rng := M{}
		for i, key := range []string{"gte", "lte"} {
			if string(pair[i]) == "null" {
				continue
			}
			v, err := scalar(Filter{Field: name, Value: pair[i]}, fld)
			if err != nil {
				return nil, false, err
			}
			rng[key] = v
		}
		if fld.Type == schema.TypeDate {
			rng["format"] = "strict_date_optional_time||epoch_millis"
		}
		return M{"range": M{name: rng}}, false, nil

	case schema.OpCIDR:
		s, err := str(f)
		if err != nil {
			return nil, false, err
		}
		if _, _, err := net.ParseCIDR(s); err != nil {
			return nil, false, badf("field %q: %q is not a CIDR block", name, s)
		}
		return M{"term": M{name: s}}, false, nil

	case schema.OpGeoDistance:
		var v struct {
			Lat, Lon float64
			Distance string
		}
		if err := json.Unmarshal(f.Value, &v); err != nil || v.Distance == "" {
			return nil, false, badf("field %q: geo_distance needs {lat, lon, distance}", name)
		}
		return M{"geo_distance": M{"distance": v.Distance, name: M{"lat": v.Lat, "lon": v.Lon}}}, false, nil

	case schema.OpGeoBBox:
		var v struct {
			TopLeft     M `json:"top_left"`
			BottomRight M `json:"bottom_right"`
		}
		if err := json.Unmarshal(f.Value, &v); err != nil || v.TopLeft == nil || v.BottomRight == nil {
			return nil, false, badf("field %q: geo_bounding_box needs {top_left:{lat,lon}, bottom_right:{lat,lon}}", name)
		}
		return M{"geo_bounding_box": M{name: M{"top_left": v.TopLeft, "bottom_right": v.BottomRight}}}, false, nil

	case schema.OpGeoPolygon:
		var pts []M
		if err := json.Unmarshal(f.Value, &pts); err != nil || len(pts) < 3 {
			return nil, false, badf("field %q: geo_polygon needs at least 3 {lat,lon} points", name)
		}
		return M{"geo_polygon": M{name: M{"points": pts}}}, false, nil

	case schema.OpGeoShape:
		var v struct {
			Shape    M      `json:"shape"`
			Relation string `json:"relation"`
		}
		if err := json.Unmarshal(f.Value, &v); err != nil || v.Shape == nil {
			return nil, false, badf("field %q: geo_shape needs {shape: <geojson>, relation?}", name)
		}
		if v.Relation == "" {
			v.Relation = "intersects"
		}
		return M{"geo_shape": M{name: M{"shape": v.Shape, "relation": v.Relation}}}, false, nil
	}
	return nil, false, badf("unsupported op %q", f.Op)
}

func str(f Filter) (string, error) {
	var s string
	if err := json.Unmarshal(f.Value, &s); err != nil || s == "" {
		return "", badf("field %q: op %s needs a non-empty string value", f.Field, f.Op)
	}
	return s, nil
}

// scalar decodes and type-checks a single value for the field.
func scalar(f Filter, fld *schema.MergedField) (any, error) {
	var v any
	if err := json.Unmarshal(f.Value, &v); err != nil || v == nil {
		return nil, badf("field %q: missing value", f.Field)
	}
	switch fld.Type {
	case schema.TypeBoolean:
		if _, ok := v.(bool); !ok {
			return nil, badf("field %q expects a boolean", f.Field)
		}
	case schema.TypeInteger, schema.TypeFloat:
		if _, ok := v.(float64); !ok {
			return nil, badf("field %q expects a number", f.Field)
		}
	case schema.TypeDate:
		switch v.(type) {
		case string, float64:
		default:
			return nil, badf("field %q expects an ISO date, epoch millis or date math", f.Field)
		}
	case schema.TypeIP:
		s, ok := v.(string)
		if !ok || net.ParseIP(s) == nil {
			return nil, badf("field %q expects an IP address", f.Field)
		}
	case schema.TypeMAC:
		s, ok := v.(string)
		if !ok {
			return nil, badf("field %q expects a MAC address", f.Field)
		}
		v = strings.ToLower(s)
	default:
		if _, ok := v.(string); !ok {
			return nil, badf("field %q expects a string", f.Field)
		}
	}
	return v, nil
}

func list(f Filter, fld *schema.MergedField) ([]any, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(f.Value, &raw); err != nil || len(raw) == 0 {
		return nil, badf("field %q: op %s needs a non-empty array", f.Field, f.Op)
	}
	out := make([]any, 0, len(raw))
	for _, r := range raw {
		v, err := scalar(Filter{Field: f.Field, Value: r}, fld)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
