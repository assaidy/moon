package moon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

const preforkChildEnv = "PREFORK_CHILD"

// IsPreforkChild reports whether the current process is a prefork child
// (see [WithPrefork]). Services are started only in child processes.
func IsPreforkChild() bool {
	return os.Getenv(preforkChildEnv) == "1"
}

// Start starts all registered services (see [App.StartServices]), then
// listens and serves HTTP. With prefork enabled it forks child processes
// instead (see [WithPrefork]). It returns [ErrFailedToStartServices] when
// any service fails to start, and nil after a graceful [App.Shutdown].
func (me *App) Start() error {
	if me.preforkIsEnabled && !IsPreforkChild() {
		return me.forkChildren()
	}

	if err := me.StartServices(); err != nil {
		return ErrFailedToStartServices
	}

	address := me.listenAddress
	if address == "" {
		if me.useTls {
			address = ":https"
		} else {
			address = ":http"
		}
	}

	listener, err := me.getListener(address)
	if err != nil {
		return err
	}

	me.logger.Info("starting server", "address", address, "pid", os.Getpid())
	if me.useTls {
		return ignoreErrServerClosed(me.httpServer.ServeTLS(listener, me.certFile, me.keyFile))
	} else {
		return ignoreErrServerClosed(me.httpServer.Serve(listener))
	}
}

var ErrFailedToStartServices = errors.New("failed to start services")

func (me *App) getListener(address string) (net.Listener, error) {
	if me.preforkIsEnabled {
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

var (
	childProcessesMutex     sync.RWMutex
	childProcesses          map[*os.Process]struct{}
	childProcessesWaitGroup sync.WaitGroup
	childProcessResultChan  chan childProcessResult
	shutdownChan            = make(chan struct{})
	shuttingDown            atomic.Bool
	allChildrenStoppedChan  = make(chan struct{})
)

func (me *App) forkChildren() error {
	childProcesses = make(map[*os.Process]struct{}, me.preforkChildrenCount)
	childProcessResultChan = make(chan childProcessResult, me.preforkChildrenCount)

	defer func() {
		childProcessesMutex.RLock()
		for proc := range childProcesses {
			me.logger.Info("stopping prefork process", "pid", proc.Pid)
			proc.Signal(syscall.SIGINT)
		}
		childProcessesMutex.RUnlock()
		childProcessesWaitGroup.Wait()
		close(allChildrenStoppedChan)
	}()

	for range me.preforkChildrenCount {
		if err := me.spawnChild(); err != nil {
			return fmt.Errorf("failed to spawn child process: %w", err)
		}
	}

	retries := 0
	for {
		select {
		case <-shutdownChan:
			shuttingDown.Store(true)
			return nil
		case result := <-childProcessResultChan:
			if shuttingDown.Load() {
				continue
			}
			me.logger.Error("a child process stopped abnormally", "pid", result.pid, "error", result.err)
			if me.preforkRetriesCount != -1 && retries == me.preforkRetriesCount {
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

	childProcessesWaitGroup.Go(func() {
		exitErr := cmd.Wait()
		childProcessesMutex.Lock()
		delete(childProcesses, cmd.Process)
		childProcessesMutex.Unlock()
		childProcessResultChan <- childProcessResult{pid: cmd.Process.Pid, err: exitErr}
	})

	childProcessesMutex.Lock()
	childProcesses[cmd.Process] = struct{}{}
	childProcessesMutex.Unlock()
	me.logger.Info("started prefork process", "pid", cmd.Process.Pid)
	return nil
}

// Shutdown gracefully shuts down the http server, then stops all started
// services (see [App.StopServices]).
func (me *App) Shutdown() error {
	defer me.StopServices()

	if !me.preforkIsEnabled || IsPreforkChild() {
		ctx := context.Background()
		if me.shutdownTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.Background(), me.shutdownTimeout)
			defer cancel()
		}
		me.logger.Info("shutting down server", "pid", os.Getpid())
		return me.httpServer.Shutdown(ctx)
	}

	close(shutdownChan)
	if me.preforkIsEnabled {
		<-allChildrenStoppedChan
	}
	return nil
}
