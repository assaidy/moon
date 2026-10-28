package moon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
)

// Start starts all registered services (see [App.StartServices]), then
// listens and serves HTTP. It returns [ErrFailedToStartServices] when
// any service fails to start, and nil after a graceful [App.Shutdown].
func (me *App) Start() error {
	if err := me.StartServices(); err != nil {
		return ErrFailedToStartServices
	}

	address := me.options.listenAddress
	if address == "" {
		if me.options.useTls {
			address = ":https"
		} else {
			address = ":http"
		}
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	me.options.logger.Info("starting server", "address", address)
	if me.options.useTls {
		return ignoreErrServerClosed(me.httpServer.ServeTLS(listener, me.options.certFile, me.options.keyFile))
	} else {
		return ignoreErrServerClosed(me.httpServer.Serve(listener))
	}
}

// RunUntilSignal starts the app, waits for one of the given signals, then
// shuts it down gracefully. When no signals are given, it waits for
// [syscall.SIGINT] and [syscall.SIGTERM]. It returns the error from
// [App.Start], or the errors from [App.Shutdown] and [App.Start] joined with
// [errors.Join] after a signal.
func (me *App) RunUntilSignal(signals ...os.Signal) error {
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	} else {
		for _, sig := range signals {
			Assert(sig != nil, "signal cannot be nil")
		}
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, signals...)
	defer signal.Stop(signalChan)

	startErrChan := make(chan error, 1)
	go func() {
		startErrChan <- me.Start()
	}()

	select {
	case err := <-startErrChan:
		return err
	case <-signalChan:
		return errors.Join(me.Shutdown(), <-startErrChan)
	}
}

var ErrFailedToStartServices = errors.New("failed to start services")

func ignoreErrServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Shutdown gracefully shuts down the http server, then stops all started
// services (see [App.StopServices]).
func (me *App) Shutdown() error {
	defer me.StopServices()

	ctx := context.Background()
	if me.options.shutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), me.options.shutdownTimeout)
		defer cancel()
	}

	me.options.logger.Info("shutting down server")
	return me.httpServer.Shutdown(ctx)
}
