package service

import (
	"sort"
	"strings"

	"github.com/kurn-dev/kurn-search/profile"
)

type registry struct {
	bindings map[string]Binding
	routes   []string
	limits   map[string]int
	// queues is each list's waiting bound: Config.MaxQueue when set, else
	// DefaultQueueFactor × the list's admission limit.
	queues map[string]int
}

func newRegistry(bindings []Binding, maxQueue int) (registry, error) {
	if len(bindings) == 0 || maxQueue < 0 || maxQueue > MaxServiceQueue {
		return registry{}, errInvalidService
	}
	result := registry{bindings: make(map[string]Binding, len(bindings)), limits: make(map[string]int), queues: make(map[string]int)}
	for _, binding := range bindings {
		if !binding.Descriptor.Valid() || nilInterface(binding.Runner) {
			return registry{}, errInvalidService
		}
		spec := binding.Descriptor.Spec()
		if !validRoute(spec.Name) || len(spec.Lists) == 0 {
			return registry{}, errInvalidService
		}
		if _, exists := result.bindings[spec.Name]; exists {
			return registry{}, errInvalidService
		}
		for _, engine := range spec.TestedEngines {
			if strings.TrimSpace(engine) == "" {
				return registry{}, errInvalidService
			}
		}
		for _, list := range spec.Lists {
			if previous, exists := result.limits[list.Name]; exists && previous != spec.Bounds.MaxInflight {
				return registry{}, errInvalidService
			}
			result.limits[list.Name] = spec.Bounds.MaxInflight
			result.queues[list.Name] = maxQueue
			if maxQueue == 0 {
				result.queues[list.Name] = DefaultQueueFactor * spec.Bounds.MaxInflight
			}
		}
		_, isFacetRunner := binding.Runner.(FacetRunner)
		_, isAlternativeRunner := binding.Runner.(AlternativeRunner)
		if isFacetRunner != binding.facets || isAlternativeRunner != binding.alternatives ||
			(binding.facets && binding.alternatives) {
			return registry{}, errInvalidService
		}
		result.bindings[spec.Name] = binding
		result.routes = append(result.routes, spec.Name)
	}
	sort.Strings(result.routes)
	return result, nil
}

func validRoute(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || strings.Contains(value, "--") {
		return false
	}
	// regexp lookahead is unsupported by Go; the explicit checks above freeze
	// the same grammar while this expression handles the character class.
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return len(value) <= 64
}

func (r registry) binding(route string) (Binding, bool) {
	binding, ok := r.bindings[route]
	return binding, ok
}

func descriptorAdmitsEngine(descriptor profile.Descriptor, engine string) bool {
	for _, tested := range descriptor.Spec().TestedEngines {
		if tested == engine {
			return true
		}
	}
	return false
}
