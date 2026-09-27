package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type rawSchema struct {
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	TimeField   string                 `json:"x-time-field"`
	Internal    bool                   `json:"x-internal"`
	Properties  map[string]rawProperty `json:"properties"`
}

type rawProperty struct {
	Type        any            `json:"type"`
	Format      string         `json:"format"`
	Description string         `json:"description"`
	Enum        []string       `json:"enum"`
	Items       *rawProperty   `json:"items"`
	Minimum     *float64       `json:"minimum"`
	Maximum     *float64       `json:"maximum"`
	OpenSearch  map[string]any `json:"x-opensearch"`
	UI          *UIHints       `json:"x-ui"`
}

// LoadDir parses every *.schema.json file in dir into a Model.
func LoadDir(dir string) ([]*Model, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no *.schema.json files in %s", dir)
	}
	var models []*Model
	for _, p := range paths {
		m, err := loadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func loadFile(path string) (*Model, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawSchema
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	name := raw.Title
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".schema.json")
	}
	m := &Model{
		Name: name, Title: raw.Title, Description: raw.Description,
		TimeField: raw.TimeField, Internal: raw.Internal, byName: map[string]*Field{},
	}
	names := make([]string, 0, len(raw.Properties))
	for n := range raw.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := toField(n, raw.Properties[n])
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", n, err)
		}
		m.Fields = append(m.Fields, f)
		m.byName[n] = f
	}
	return m, nil
}

// toField converts one JSONSchema property to a Field and its OpenSearch mapping.
func toField(name string, p rawProperty) (*Field, error) {
	f := &Field{Name: name, Description: p.Description, Enum: p.Enum, UI: p.UI, Min: p.Minimum, Max: p.Maximum}
	prop := p
	if jsonType(p.Type) == "array" && p.Items != nil {
		f.Array = true
		prop = *p.Items
		prop.OpenSearch = firstNonNil(p.OpenSearch, p.Items.OpenSearch)
		if len(prop.Enum) == 0 {
			prop.Enum = p.Enum
		}
	}

	osType, _ := prop.OpenSearch["type"].(string)
	switch {
	case osType != "":
		f.Type = fromOpenSearchType(osType)
	case prop.Format == "date-time" || prop.Format == "date":
		f.Type = TypeDate
	case prop.Format == "ipv4" || prop.Format == "ipv6" || prop.Format == "ip":
		f.Type = TypeIP
	case prop.Format == "mac":
		f.Type = TypeMAC
	default:
		switch jsonType(prop.Type) {
		case "string":
			f.Type = TypeKeyword
		case "integer":
			f.Type = TypeInteger
		case "number":
			f.Type = TypeFloat
		case "boolean":
			f.Type = TypeBoolean
		case "object":
			f.Type = TypeObject
		default:
			return nil, fmt.Errorf("unsupported json type %v", prop.Type)
		}
	}
	if f.Type == TypeVector {
		dim, _ := prop.OpenSearch["dimension"].(float64)
		if dim == 0 {
			return nil, fmt.Errorf("knn_vector requires x-opensearch.dimension")
		}
		f.Dimension = int(dim)
	}
	f.Ops = OpsFor(f.Type)
	f.Sortable = sortable[f.Type] && !f.Array
	f.Aggregatable = aggregatable[f.Type]
	f.Searchable = f.Type == TypeText || (f.Type == TypeKeyword && len(f.Enum) == 0)
	f.Mapping = buildMapping(f, prop.OpenSearch)
	return f, nil
}

func buildMapping(f *Field, extra map[string]any) map[string]any {
	m := map[string]any{}
	switch f.Type {
	case TypeKeyword:
		m["type"] = "keyword"
	case TypeMAC:
		m["type"] = "keyword"
		m["normalizer"] = "lowercase"
	case TypeText:
		m["type"] = "text"
		m["fields"] = map[string]any{"keyword": map[string]any{"type": "keyword", "ignore_above": 1024}}
	case TypeBoolean:
		m["type"] = "boolean"
	case TypeInteger:
		m["type"] = "long"
	case TypeFloat:
		m["type"] = "double"
	case TypeDate:
		m["type"] = "date"
	case TypeIP:
		m["type"] = "ip"
	case TypeGeoPoint:
		m["type"] = "geo_point"
	case TypeGeoShape:
		m["type"] = "geo_shape"
	case TypeVector:
		m["type"] = "knn_vector"
		m["dimension"] = f.Dimension
		m["method"] = map[string]any{"name": "hnsw", "space_type": "cosinesimil", "engine": "lucene"}
	case TypeObject:
		m["type"] = "object"
	}
	for k, v := range extra {
		if k != "type" {
			m[k] = v
		}
	}
	return m
}

func fromOpenSearchType(t string) FieldType {
	switch t {
	case "keyword":
		return TypeKeyword
	case "text", "search_as_you_type":
		return TypeText
	case "boolean":
		return TypeBoolean
	case "long", "integer", "short", "byte":
		return TypeInteger
	case "double", "float", "half_float", "scaled_float":
		return TypeFloat
	case "date", "date_nanos":
		return TypeDate
	case "ip":
		return TypeIP
	case "geo_point":
		return TypeGeoPoint
	case "geo_shape":
		return TypeGeoShape
	case "knn_vector":
		return TypeVector
	default:
		return TypeObject
	}
}

func jsonType(t any) string {
	switch v := t.(type) {
	case string:
		return v
	case []any: // e.g. ["string","null"]
		for _, x := range v {
			if s, _ := x.(string); s != "" && s != "null" {
				return s
			}
		}
	}
	return ""
}

func firstNonNil(a, b map[string]any) map[string]any {
	if a != nil {
		return a
	}
	return b
}

// IndexMapping renders the full index body (settings + mappings) for a model.
func (m *Model) IndexMapping() map[string]any {
	props := map[string]any{}
	hasVector := false
	for _, f := range m.Fields {
		props[f.Name] = f.Mapping
		if f.Type == TypeVector {
			hasVector = true
		}
	}
	settings := map[string]any{
		"number_of_shards":   1,
		"number_of_replicas": 0,
		"analysis": map[string]any{
			"normalizer": map[string]any{
				"lowercase": map[string]any{"type": "custom", "filter": []string{"lowercase"}},
			},
		},
	}
	if hasVector {
		settings["index.knn"] = true
	}
	return map[string]any{"settings": settings, "mappings": map[string]any{"properties": props}}
}
