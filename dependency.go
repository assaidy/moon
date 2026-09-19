package moon

import "reflect"

// AddDependency registers a dependency that can be retrieved through [Context.GetDependency].
// If a dependency of the same type is already registered, it is replaced.
// The dependency must not be nil.
func (me *App) AddDependency(d any) {
	Assert(!isNil(d), "dependency cannot be nil")
	me.dependencies[reflect.TypeOf(d)] = d
}

// GetDependency returns the dependency registered for T through
// [App.AddDependency]. It panics when no dependency of that type
// was registered.
func (me *Context) GetDependency[T any]() T {
	t := reflect.TypeFor[T]()
	d, ok := me.dependencies[t].(T)
	Assert(ok, "dependency not found: "+t.String())
	return d
}
