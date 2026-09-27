package schema

import (
	"fmt"
	"sort"
	"strings"
)

// Registry maps model names to index names and merges schemas across models.
type Registry struct {
	prefix string
	models map[string]*Model
	order  []string
}

// NewRegistry builds a registry for the given prefix.
func NewRegistry(prefix string, models []*Model) *Registry {
	r := &Registry{prefix: prefix, models: map[string]*Model{}}
	for _, m := range models {
		r.models[m.Name] = m
		r.order = append(r.order, m.Name)
	}
	return r
}

// Prefix returns the configured index prefix.
func (r *Registry) Prefix() string { return r.prefix }

// Model returns a model by name.
func (r *Registry) Model(name string) (*Model, bool) {
	m, ok := r.models[name]
	return m, ok
}

// Models returns all models in name order.
func (r *Registry) Models() []*Model {
	out := make([]*Model, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.models[n])
	}
	return out
}

// Public returns models that are not internal, in name order.
func (r *Registry) Public() []*Model {
	var out []*Model
	for _, m := range r.Models() {
		if !m.Internal {
			out = append(out, m)
		}
	}
	return out
}

// IndexName returns the prefixed index for a model.
func (r *Registry) IndexName(model string) string { return r.prefix + model }

// ModelName strips the prefix from an index name.
func (r *Registry) ModelName(index string) string { return strings.TrimPrefix(index, r.prefix) }

// Resolve validates model names and returns the models and prefixed indices.
// An empty list means all public models.
func (r *Registry) Resolve(names []string) ([]*Model, []string, error) {
	if len(names) == 0 {
		ms := r.Public()
		idx := make([]string, len(ms))
		for i, m := range ms {
			idx[i] = r.IndexName(m.Name)
		}
		return ms, idx, nil
	}
	seen := map[string]bool{}
	var ms []*Model
	var idx []string
	for _, n := range names {
		m, ok := r.models[n]
		if !ok || m.Internal {
			return nil, nil, fmt.Errorf("unknown model %q", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		ms = append(ms, m)
		idx = append(idx, r.IndexName(n))
	}
	return ms, idx, nil
}

// Merge builds the cross-model field view for a set of models.
func Merge(models []*Model) []*MergedField {
	byName := map[string]*MergedField{}
	var order []string
	for _, m := range models {
		for _, f := range m.Fields {
			mf, ok := byName[f.Name]
			if !ok {
				cp := *f
				mf = &MergedField{Field: cp, Types: map[string]FieldType{}}
				byName[f.Name] = mf
				order = append(order, f.Name)
			}
			mf.Models = append(mf.Models, m.Name)
			mf.Types[m.Name] = f.Type
			if f.Type != mf.Type {
				mf.Conflict = true
			}
			mf.Enum = union(mf.Enum, f.Enum)
			if mf.Description == "" {
				mf.Description = f.Description
			}
		}
	}
	sort.Strings(order)
	out := make([]*MergedField, 0, len(order))
	for _, n := range order {
		mf := byName[n]
		if mf.Conflict {
			mf.Ops = []Op{OpExists, OpNotExists}
			mf.Sortable, mf.Aggregatable, mf.Searchable = false, false, false
		} else {
			mf.Types = nil
		}
		out = append(out, mf)
	}
	return out
}

// Lookup returns the merged field by name.
func Lookup(fields []*MergedField, name string) *MergedField {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func union(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
