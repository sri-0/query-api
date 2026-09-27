package schema

// opsByType is the single source of truth for what each field type supports.
var opsByType = map[FieldType][]Op{
	TypeKeyword:  {OpEq, OpNe, OpIn, OpNotIn, OpPrefix, OpWildcard, OpExists, OpNotExists},
	TypeMAC:      {OpEq, OpNe, OpIn, OpNotIn, OpPrefix, OpExists, OpNotExists},
	TypeText:     {OpMatch, OpMatchPhrase, OpWildcard, OpExists, OpNotExists},
	TypeBoolean:  {OpEq, OpExists, OpNotExists},
	TypeInteger:  {OpEq, OpNe, OpIn, OpNotIn, OpGt, OpGte, OpLt, OpLte, OpBetween, OpExists, OpNotExists},
	TypeFloat:    {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte, OpBetween, OpExists, OpNotExists},
	TypeDate:     {OpGt, OpGte, OpLt, OpLte, OpBetween, OpExists, OpNotExists},
	TypeIP:       {OpEq, OpNe, OpIn, OpNotIn, OpCIDR, OpBetween, OpExists, OpNotExists},
	TypeGeoPoint: {OpGeoDistance, OpGeoBBox, OpGeoPolygon, OpExists, OpNotExists},
	TypeGeoShape: {OpGeoShape, OpExists, OpNotExists},
	TypeVector:   {},
	TypeObject:   {OpExists, OpNotExists},
}

var sortable = map[FieldType]bool{
	TypeKeyword: true, TypeMAC: true, TypeBoolean: true, TypeInteger: true,
	TypeFloat: true, TypeDate: true, TypeIP: true,
}

var aggregatable = map[FieldType]bool{
	TypeKeyword: true, TypeMAC: true, TypeBoolean: true, TypeInteger: true,
	TypeFloat: true, TypeDate: true, TypeIP: true,
}

// Supports reports whether op is valid for the field.
func (f *Field) Supports(op Op) bool {
	for _, o := range f.Ops {
		if o == op {
			return true
		}
	}
	return false
}

// Supported ops
func OpsFor(t FieldType) []Op { return opsByType[t] }
