package bridge

import "sort"

// Constructor creates a Bridge instance, returning an error if config is missing (e.g. API key).
type Constructor func() (Bridge, error)

// Registry maps provider type strings to their constructors.
type Registry struct {
	providers map[string]Constructor
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Constructor)}
}

func (r *Registry) Register(name string, c Constructor) {
	r.providers[name] = c
}

func (r *Registry) Get(name string) (Constructor, bool) {
	c, ok := r.providers[name]
	return c, ok
}

func (r *Registry) Providers() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
