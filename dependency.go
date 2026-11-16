package moon

import (
	"fmt"
	"reflect"
)

// AddDependency registers a dependency that can be retrieved through [Context.GetDependency].
// The dependency must not be nil.
// It panics if a dependency of the same type is already registered.
func (me *App) AddDependency(d any) {
	Assert(!IsNilValue(d), "dependency cannot be nil")
	t := reflect.TypeOf(d)
	_, exists := me.dependencies[t]
	Assert(!exists, fmt.Sprintf("dependency of type: %s is already registered", t.String()))
	me.dependencies[t] = d
}

// GetDependency returns the dependency registered for T through
// [App.AddDependency]. It panics when no dependency of that type
// was registered.
func (me *Context) GetDependency[T any]() T {
	t := reflect.TypeFor[T]()
	d, ok := me.app.dependencies[t].(T)
	Assert(ok, "dependency not found: "+t.String())
	return d
}
