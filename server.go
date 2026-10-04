package moon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

const preforkChildEnv = "PREFORK_CHILD"

// IsPreforkChild reports whether the current process is a prefork child
// (see [App.WithPrefork]). Services are started only in child processes.
func IsPreforkChild() bool {
	return os.Getenv(preforkChildEnv) == "1"
}

// Start starts all registered services (see [App.StartServices]), then
// listens and serves HTTP. With prefork enabled it forks child processes
// instead (see [App.WithPrefork]). It returns [ErrFailedToStartServices] when
// any service fails to start, and nil after a graceful [App.Shutdown].
func (me *App) Start() error {
	if me.options.preforkIsEnabled && !IsPreforkChild() {
		return me.forkChildren()
	}

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

	listener, err := me.getListener(address)
	if err != nil {
		return err
	}

	me.options.logger.Info("starting server", "address", address, "pid", os.Getpid())
	if me.options.useTls {
		return ignoreErrServerClosed(me.httpServer.ServeTLS(listener, me.options.certFile, me.options.keyFile))
	} else {
		return ignoreErrServerClosed(me.httpServer.Serve(listener))
	}
}

var ErrFailedToStartServices = errors.New("failed to start services")

func (me *App) getListener(address string) (net.Listener, error) {
	if me.options.preforkIsEnabled {
		config := net.ListenConfig{
			Control: func(network, address string, c syscall.RawConn) error {
				return c.Control(func(fd uintptr) {
					syscall.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
				})
			},
		}
		return config.Listen(context.Background(), "tcp", address)
	}

	return net.Listen("tcp", address)
}

func ignoreErrServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

type childProcessResult struct {
	pid int
	err error
}

func (me *App) forkChildren() error {
	me.childProcesses = make(map[*os.Process]struct{}, me.options.preforkChildrenCount)
	me.childProcessResultChan = make(chan childProcessResult, me.options.preforkChildrenCount)

	defer func() {
		me.childProcessesMutex.RLock()
		for proc := range me.childProcesses {
			me.options.logger.Info("stopping prefork process", "pid", proc.Pid)
			proc.Signal(syscall.SIGINT)
		}
		me.childProcessesMutex.RUnlock()
		me.childProcessesWaitGroup.Wait()
		close(me.allChildrenStoppedChan)
	}()

	for range me.options.preforkChildrenCount {
		if err := me.spawnChild(); err != nil {
			return fmt.Errorf("failed to spawn child process: %w", err)
		}
	}

	retries := 0
	for {
		select {
		case <-me.shutdownChan:
			me.shuttingDown.Store(true)
			return nil
		case result := <-me.childProcessResultChan:
			if me.shuttingDown.Load() {
				continue
			}
			me.options.logger.Error("a child process stopped abnormally", "pid", result.pid, "error", result.err)
			if me.options.preforkRetriesCount != -1 && retries == me.options.preforkRetriesCount {
				return ErrPreforkRetriesExceeded
			}
			if err := me.spawnChild(); err != nil {
				return fmt.Errorf("failed to spawn child process: %w", err)
			}
			retries++
		}
	}
}

var ErrPreforkRetriesExceeded = errors.New("prefork retries exceeded")

func (me *App) spawnChild() error {
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), preforkChildEnv+"=1")
	// put each child in its own process group so terminal-generated signals
	// (e.g. Ctrl-C/SIGINT) go only to the parent. The parent then relays
	// shutdown explicitly via proc.Signal. Otherwise children would receive
	// SIGINT twice, and the second one could kill them mid-shutdown after
	// cancel() has restored the default signal disposition.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	me.childProcessesWaitGroup.Go(func() {
		exitErr := cmd.Wait()
		me.childProcessesMutex.Lock()
		delete(me.childProcesses, cmd.Process)
		me.childProcessesMutex.Unlock()
		me.childProcessResultChan <- childProcessResult{pid: cmd.Process.Pid, err: exitErr}
	})

	me.childProcessesMutex.Lock()
	me.childProcesses[cmd.Process] = struct{}{}
	me.childProcessesMutex.Unlock()
	me.options.logger.Info("started prefork process", "pid", cmd.Process.Pid)
	return nil
}

// Shutdown gracefully shuts down the http server, then stops all started
// services (see [App.StopServices]).
func (me *App) Shutdown() error {
	defer me.StopServices()

	if !me.options.preforkIsEnabled || IsPreforkChild() {
		ctx := context.Background()
		if me.options.shutdownTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), me.options.shutdownTimeout)
			defer cancel()
		}
		me.options.logger.Info("shutting down server", "pid", os.Getpid())
		return me.httpServer.Shutdown(ctx)
	}

	close(me.shutdownChan)
	if me.options.preforkIsEnabled {
		<-me.allChildrenStoppedChan
	}
	return nil
}
