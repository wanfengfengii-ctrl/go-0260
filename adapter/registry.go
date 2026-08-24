package adapter

import "cacaoferment/evidence"

// Registry resolves the instrument adapters used by the service by kind. It is
// seeded by the executable and read at the equipment-start, toxin, and
// chemistry steps.
type Registry struct {
	byKind map[evidence.AdapterKind]*ScriptAdapter
}

// NewRegistry returns an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{byKind: make(map[evidence.AdapterKind]*ScriptAdapter)}
}

// Register installs an adapter for a specific kind, replacing any previous one.
func (r *Registry) Register(a *ScriptAdapter) {
	r.byKind[a.Kind()] = a
}

// Get returns the adapter registered for a kind, or nil.
func (r *Registry) Get(kind evidence.AdapterKind) *ScriptAdapter {
	return r.byKind[kind]
}

// Healthy reports whether every required instrument kind is registered. The
// three kinds map to the probe, toxin plate reader, and moisture meter.
func (r *Registry) Healthy() bool {
	for _, k := range []evidence.AdapterKind{
		evidence.AdapterProbe,
		evidence.AdapterToxinReader,
		evidence.AdapterMoistureMeter,
	} {
		if r.byKind[k] == nil {
			return false
		}
	}
	return true
}
