package moon

import (
	"reflect"
	"slices"
)

// Injected gives service constructors access to the app's registered
// dependencies and services that have already started. The app supplies it
// when a constructor runs.
type Injected struct {
	app *App
}

// GetService returns the started service of type T.
// It panics if T was not registered or has not started yet, or if Injected was
// not supplied by the app.
func (me Injected) GetService[T Service]() T {
	Assert(me.app != nil, "Injected must be supplied by moon")
	t := reflect.TypeFor[T]()
	i := slices.IndexFunc(me.app.services, func(s serviceInfo) bool {
		return s.type_ == t
	})
	Assert(i != -1, "service not found: "+t.String())
	s := me.app.services[i]
	Assert(s.started && !IsNilValue(s.value), "service didn't start: "+t.String())
	return s.value.(T)
}

// GetDependency returns the dependency of type T registered through
// [App.AddDependency]. It panics if no dependency of that type was registered,
// or if Injected was not supplied by the app.
func (me Injected) GetDependency[T any]() T {
	Assert(me.app != nil, "Injected must be supplied by moon")
	t := reflect.TypeFor[T]()
	d, ok := me.app.dependencies[t].(T)
	Assert(ok, "dependency not found: "+t.String())
	return d
}
