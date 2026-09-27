package moon

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Service is a managed background resource owned by the [App].
// Services are started automatically by [App.Start] (see [App.StartServices])
// and stopped automatically by [App.Shutdown] (see [App.StopServices]).
// Start and Stop must respect context cancellation and timeouts.
type Service interface {
	// Name returns a human-readable service name used in logs.
	Name() string

	// Start initializes the service.
	Start(context.Context) error

	// Stop shuts the service down.
	Stop(context.Context) error

	// IsAvailable reports whether the service is currently available for use.
	IsAvailable() bool

	// TODO: create a utility to track availability inside services.
}

type serviceInfo struct {
	type_   reflect.Type
	started bool
	value   any
}

// AddService registers a service that becomes retrievable through
// [Context.GetService] only after it has been successfully started
// (see [App.StartServices]).
// It panics if a service of the same type is already registered.
// The service must not be nil.
func (me *App) AddService[T Service](s T) {
	Assert(!isNil(s), "service cannot be nil")
	t := reflect.TypeOf(s)
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

// StartServices starts all registered services according to the configured
// start timeout ([App.WithServiceStartTimeout]) and mode ([App.WithParallelServiceStart]).
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
	// in prefork mode, don't start services in parent process.
	// it doesn't listen for requests. it just starts children.
	if me.preforkIsEnabled && !IsPreforkChild() {
		return nil
	}

	me.logger.Info("starting all services", "pid", os.Getpid())

	if me.serviceStartParallel {
		return me.startServicesParallel()
	} else {
		return me.startServicesSequential()
	}
}

func (me *App) startServicesSequential() error {
	for i, s := range me.services {
		if err := me.startOneService(s.value.(Service)); err != nil {
			me.StopServices()
			return err
		}
		me.services[i].started = true
	}

	me.logger.Info("all services started")
	return nil
}

func (me *App) startServicesParallel() error {
	var wg errgroup.Group
	var mu sync.Mutex

	for i, s := range me.services {
		wg.Go(func() error {
			if err := me.startOneService(s.value.(Service)); err != nil {
				return err
			}
			mu.Lock()
			me.services[i].started = true
			mu.Unlock()
			return nil
		})
	}

	if err := wg.Wait(); err != nil {
		me.StopServices()
		return err
	}

	me.logger.Info("all services started")
	return nil
}

func (me *App) startOneService(service Service) error {
	ctx := context.Background()
	if me.serviceStartTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, me.serviceStartTimeout)
		defer cancel()
	}

	if err := service.Start(ctx); err != nil {
		me.logger.Error("failed to start service", "name", service.Name(), "error", err, "pid", os.Getpid())
		return err
	}

	me.logger.Info("service started", "name", service.Name(), "pid", os.Getpid())
	return nil
}

// StopServices stops all started services according to the configured
// stop timeout ([App.WithServiceStopTimeout]) and mode ([App.WithParallelServiceStop]).
//
// It is called automatically by [App.Shutdown]. It is exported so tests that
// started services via [App.StartServices] can stop them without ever
// listening.
func (me *App) StopServices() {
	// see comment in [App.StartServices]
	if me.preforkIsEnabled && !IsPreforkChild() {
		return
	}

	me.logger.Info("stopping all services", "pid", os.Getpid())

	if me.serviceStopParallel {
		me.stopServicesParallel()
	} else {
		me.stopServicesSequential()
	}
}

func (me *App) stopServicesSequential() {
	for i, s := range me.services {
		if s.started {
			me.stopOneService(s.value.(Service))
			me.services[i].started = false
		}
	}
	me.logger.Info("all services stopped")
}

func (me *App) stopServicesParallel() {
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, s := range me.services {
		if s.started {
			wg.Go(func() {
				me.stopOneService(s.value.(Service))
				mu.Lock()
				me.services[i].started = false
				mu.Unlock()
			})
		}
	}

	wg.Wait()
	me.logger.Info("all services stopped")
}

func (me *App) stopOneService(service Service) {
	ctx := context.Background()
	if me.serviceStopTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, me.serviceStopTimeout)
		defer cancel()
	}

	if err := service.Stop(ctx); err != nil {
		me.logger.Error("failed to stop service", "name", service.Name(), "error", err, "pid", os.Getpid())
		return
	}

	me.logger.Info("service stopped", "name", service.Name(), "pid", os.Getpid())
}
