package moon

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
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
	value   any
	started bool
}

// AddService registers a service that becomes retrievable through
// [Context.GetService] only after it has been successfully started
// (see [App.StartServices]).
// It panics if a service of the same type is already registered.
// The service must not be nil.
func (me *App) AddService[T Service](s T) {
	Assert(!IsNilValue(s), "service cannot be nil")
	t := reflect.TypeOf(s)
	Assert(strings.TrimSpace(s.Name()) != "", "service name cannot be empty or whitespace")
	Assert(
		slices.IndexFunc(me.services, func(s serviceInfo) bool { return s.type_ == t }) == -1,
		fmt.Sprintf("service of type: %s is already registered", t.String()),
	)

	me.services = append(me.services, serviceInfo{type_: t, value: s})
}

// GetService returns the started service for T (see [App.AddService] and
// [App.StartServices]).
// It panics when no service of that type was registered, or when it was
// registered but never successfully started.
func (me *Context) GetService[T Service]() T {
	t := reflect.TypeFor[T]()
	i := slices.IndexFunc(me.app.services, func(s serviceInfo) bool { return s.type_ == t })
	Assert(i != -1, "service not found: "+t.String())
	s := me.app.services[i]
	Assert(s.started, "service didn't start: "+t.String())
	return s.value.(T)
}

// StartServices starts all registered services in registration order.
// Services that are already started are left untouched.
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

	for i, s := range me.services {
		if !s.started {
			service := s.value.(Service)
			if err := service.Start(); err != nil {
				me.options.logger.Error("failed to start service", "name", service.Name(), "error", err)
				me.StopServices()
				return fmt.Errorf("failed to start %q service: %w", service.Name(), err)
			}
			me.services[i].started = true
		}
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
		if s.started {
			service := s.value.(Service)
			if err := service.Stop(); err != nil {
				me.options.logger.Error("failed to stop service", "name", service.Name(), "error", err)
				continue
			}
			me.options.logger.Info("service stopped", "name", service.Name())
			me.services[i].started = false
		}
	}

	me.options.logger.Info("all services stopped")
}
