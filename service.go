package moon

import (
	"fmt"
	"reflect"
	"slices"
)

// Service is a managed background resource owned by the [App].
// Services are started automatically by [App.Start] (see [App.StartServices])
// and stopped automatically by [App.Shutdown] (see [App.StopServices]).
type Service interface {
	// Name returns a human-readable service name used in logs.
	Name() string

	// Start initializes the service.
	Start() error

	// Stop shuts the service down.
	Stop() error

	// IsReady reports whether the service is ready for use.
	IsReady() bool
}

// ServiceConstructor builds a service of type T when the service starts.
// It receives an [Injected] that exposes registered dependencies and services
// which have already started.
//
// A service package can return one from its New function, so registration
// stays short: app.AddService(postgres.New(url)).
type ServiceConstructor[T Service] func(Injected) T

// CheckServicesReadiness reports whether every given service is non-nil
// and ready (see [Service.IsReady]). It returns true when called with no
// services.
func CheckServicesReadiness(services ...Service) bool {
	for _, s := range services {
		if s == nil || !s.IsReady() {
			return false
		}
	}
	return true
}

// CheckServicesReadiness reports whether every registered service has started
// and is ready (see [Service.IsReady]). It returns true when no services are
// registered.
func (me *App) CheckServicesReadiness() bool {
	for _, s := range me.services {
		if !s.started || !s.value.(Service).IsReady() {
			return false
		}
	}
	return true
}

type serviceInfo struct {
	type_   reflect.Type
	build   func(Injected) any
	value   any
	started bool
}

// AddService registers a [ServiceConstructor] for the service type T.
// The constructor runs when the service is started (see [App.StartServices]),
// in registration order, and receives an [Injected] that exposes registered
// dependencies and services which have already started. This lets later
// services depend on earlier ones.
//
// build can be a [ServiceConstructor] or any function type with the same
// signature, so a service's New function can return its constructor directly:
//
//	app.AddService(postgres.New(os.Getenv("POSTGRES_URL")))
//
// It panics if build is nil or a service of the same type is already
// registered.
func (me *App) AddService[T Service, F ~func(Injected) T](build F) {
	Assert(build != nil, "service constructor cannot be nil")
	t := reflect.TypeFor[T]()
	Assert(
		slices.IndexFunc(me.services, func(s serviceInfo) bool { return s.type_ == t }) == -1,
		fmt.Sprintf("service of type: %s is already registered", t.String()),
	)
	me.services = append(me.services, serviceInfo{
		type_: t,
		build: func(injected Injected) any { return build(injected) },
	})
}

// GetService returns the started service for T (see [App.AddService] and
// [App.StartServices]).
// It panics when no service of that type was registered, or when it was
// registered but never successfully started.
func (me *Context) GetService[T Service]() T {
	return Injected{app: me.app}.GetService[T]()
}

// StartServices constructs and starts all registered services in registration
// order. Constructors can retrieve dependencies and services that have already
// started through [Injected]. Services that are already started are left
// untouched, so their constructors do not run again.
//
// If any service fails to start, already-started services are stopped and
// discarded; the start error is returned.
//
// Only successfully started services become visible to [Context.GetService]
// and are stopped by [App.StopServices].
//
// It is called automatically by [App.Start]. It is exported so tests can
// start services without listening, e.g. TestMain or TestXxx setups that
// exercise handlers via [App.Test] without binding a port.
func (me *App) StartServices() error {
	me.options.logger.Info("starting all services")

	injected := Injected{app: me}

	for i, s := range me.services {
		if s.started {
			continue
		}
		value := s.build(injected)
		Assert(!IsNilValue(value), "service constructor returned nil: "+s.type_.String())
		service := value.(Service)
		if err := service.Start(); err != nil {
			me.options.logger.Error("failed to start service", "name", service.Name(), "error", err)
			me.StopServices()
			return fmt.Errorf("failed to start %q service: %w", service.Name(), err)
		}
		me.services[i].value = value
		me.services[i].started = true
		me.options.logger.Info("service started", "name", service.Name())
	}

	me.options.logger.Info("all services started")
	return nil
}

// StopServices stops all started services in reverse registration order, so
// services that depend on earlier services are stopped before their
// dependencies. A failed stop is logged and leaves the service marked as
// started.
//
// It is called automatically by [App.Shutdown]. It is exported so tests that
// started services via [App.StartServices] can stop them without ever
// listening.
func (me *App) StopServices() {
	me.options.logger.Info("stopping all services")

	// stop in reverse registration order so services that depend on earlier
	// services are stopped before their dependencies.
	for i, s := range slices.Backward(me.services) {
		if !s.started {
			continue
		}
		service := s.value.(Service)
		if err := service.Stop(); err != nil {
			me.options.logger.Error("failed to stop service", "name", service.Name(), "error", err)
			continue
		}
		me.services[i].started = false
		me.options.logger.Info("service stopped", "name", service.Name())
	}

	me.options.logger.Info("all services stopped")
}
