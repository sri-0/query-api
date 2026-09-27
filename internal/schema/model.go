// Package schema loads JSONSchema model definitions and derives the normalized
// field model that drives query validation, index mappings and the UI.
package schema

// FieldType is the normalized type exposed to clients.
type FieldType string

const (
	TypeKeyword  FieldType = "keyword"
	TypeText     FieldType = "text"
	TypeBoolean  FieldType = "boolean"
	TypeInteger  FieldType = "integer"
	TypeFloat    FieldType = "float"
	TypeDate     FieldType = "date"
	TypeIP       FieldType = "ip"
	TypeMAC      FieldType = "mac"
	TypeGeoPoint FieldType = "geo_point"
	TypeGeoShape FieldType = "geo_shape"
	TypeVector   FieldType = "vector"
	TypeObject   FieldType = "object"
)

// Op is a filter operator.
type Op string

const (
	OpEq          Op = "eq"
	OpNe          Op = "ne"
	OpIn          Op = "in"
	OpNotIn       Op = "not_in"
	OpPrefix      Op = "prefix"
	OpWildcard    Op = "wildcard"
	OpMatch       Op = "match"
	OpMatchPhrase Op = "match_phrase"
	OpGt          Op = "gt"
	OpGte         Op = "gte"
	OpLt          Op = "lt"
	OpLte         Op = "lte"
	OpBetween     Op = "between"
	OpCIDR        Op = "cidr"
	OpGeoDistance Op = "geo_distance"
	OpGeoBBox     Op = "geo_bounding_box"
	OpGeoPolygon  Op = "geo_polygon"
	OpGeoShape    Op = "geo_shape"
	OpExists      Op = "exists"
	OpNotExists   Op = "not_exists"
)

// UIHints are optional presentation hints carried from the JSONSchema x-ui block.
type UIHints struct {
	Label          string `json:"label,omitempty"`
	Cell           string `json:"cell,omitempty"`
	Hidden         bool   `json:"hidden,omitempty"`
	DefaultVisible *bool  `json:"defaultVisible,omitempty"`
}

// Field is a normalized field of a model.
type Field struct {
	Name         string    `json:"name"`
	Type         FieldType `json:"type"`
	Description  string    `json:"description,omitempty"`
	Array        bool      `json:"array,omitempty"`
	Enum         []string  `json:"enum,omitempty"`
	Ops          []Op      `json:"ops"`
	Sortable     bool      `json:"sortable"`
	Aggregatable bool      `json:"aggregatable"`
	Searchable   bool      `json:"searchable"` // participates in full-text "text" search
	Dimension    int       `json:"dimension,omitempty"`
	Min          *float64  `json:"min,omitempty"`
	Max          *float64  `json:"max,omitempty"`
	UI           *UIHints  `json:"ui,omitempty"`
	// Mapping is the OpenSearch property mapping generated for this field.
	Mapping map[string]any `json:"-"`
}

// Model is one JSONSchema file, i.e. one index.
type Model struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	TimeField   string   `json:"timeField,omitempty"`
	Internal    bool     `json:"internal,omitempty"`
	Fields      []*Field `json:"fields"`
	byName      map[string]*Field
}

// Field returns the named field or nil.
func (m *Model) Field(name string) *Field { return m.byName[name] }

// VectorField returns the first knn_vector field or nil.
func (m *Model) VectorField() *Field {
	for _, f := range m.Fields {
		if f.Type == TypeVector {
			return f
		}
	}
	return nil
}

// MergedField is a field as seen across a set of models.
type MergedField struct {
	Field
	Models   []string             `json:"models"`
	Conflict bool                 `json:"conflict,omitempty"`
	Types    map[string]FieldType `json:"types,omitempty"` // model -> type, only when conflict
}
