package moon

import (
	"context"
	"os"
	"reflect"
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
}

// AddService registers a service that can be retrieved through [Context.GetService].
// If a service of the same type is already registered, it is replaced.
// The service must not be nil.
func (me *App) AddService[T Service](s T) {
	Assert(!isNil(s), "service cannot be nil")
	me.services[reflect.TypeOf(s)] = s
}

// GetService returns the service registered for T through [App.AddService].
// It panics when no service of that type was registered.
func (me *Context) GetService[T Service]() T {
	t := reflect.TypeFor[T]()
	s, ok := me.services[t].(T)
	Assert(ok, "service not found: "+t.String())
	return s
}

// StartServices starts all registered services according to the configured
// start timeout ([WithServiceStartTimeout]) and mode ([WithParallelServiceStart]).
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
	started := make(map[reflect.Type]any, len(me.services))
	original := me.services

	for t, s := range me.services {
		if err := me.startOneService(s.(Service)); err != nil {
			me.services = started
			me.StopServices()
			me.services = original
			return err
		}
		started[t] = s
	}

	me.logger.Info("all services started")
	return nil
}

func (me *App) startServicesParallel() error {
	started := make(map[reflect.Type]any, len(me.services))
	original := me.services
	var wg errgroup.Group
	var mu sync.Mutex

	for t, s := range me.services {
		wg.Go(func() error {
			if err := me.startOneService(s.(Service)); err != nil {
				return err
			}
			mu.Lock()
			started[t] = s
			mu.Unlock()
			return nil
		})
	}

	if err := wg.Wait(); err != nil {
		me.services = started
		me.StopServices()
		me.services = original
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

// StopServices stops all registered services according to the configured
// stop timeout ([WithServiceStopTimeout]) and mode ([WithParallelServiceStop]).
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
	for _, s := range me.services {
		me.stopOneService(s.(Service))
	}
	me.logger.Info("all services stopped")
}

func (me *App) stopServicesParallel() {
	var wg sync.WaitGroup
	for _, s := range me.services {
		wg.Go(func() {
			me.stopOneService(s.(Service))
		})
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
