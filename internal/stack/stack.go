// Package stack describes a running application in terms Bopper cares about,
// independently of how it was declared.
//
// Compose is the first backend, not the model. Everything above this package -
// the workspace layer, change detection's output, the data layer, the CLI -
// speaks in these terms, so a second backend replaces one implementation rather
// than threading a new set of types through the whole tool.
//
// The fields here are deliberately the ones Bopper actually uses. A backend
// keeps whatever richer description it needs to its own package; if something
// belongs in this struct, it is because a decision above depends on it.
package stack

import "sort"

// Service is one component of an application.
type Service struct {
	Name string
	// Image is what the service runs when it is not built from source.
	Image string
	// Builds reports whether this service is built from the repository, which
	// is what makes it a candidate for an overlay at all.
	Builds bool
	// Ports are container ports worth reaching, published or merely exposed.
	// The first is what a proxy routes to.
	Ports []int
	// Env is the service's resolved environment. The data layer reads database
	// credentials from it.
	Env map[string]string
}

// Stack is an application: a set of services and where they were described.
type Stack struct {
	Dir      string
	Services []Service
}

// Names returns every service name, sorted.
func (s Stack) Names() []string {
	out := make([]string, 0, len(s.Services))
	for _, svc := range s.Services {
		out = append(out, svc.Name)
	}
	sort.Strings(out)
	return out
}

// Get returns one service by name.
func (s Stack) Get(name string) (Service, bool) {
	for _, svc := range s.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return Service{}, false
}

// Len is the number of services.
func (s Stack) Len() int { return len(s.Services) }
