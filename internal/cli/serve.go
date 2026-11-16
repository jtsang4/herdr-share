package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jtsang4/herdr-share/internal/bridge"
	"github.com/jtsang4/herdr-share/internal/control"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/store"
)

func (a *app) serve(ctx context.Context, s serveArgs) error {
	if ctx == nil {
		ctx = context.Background()
	}
	fail := func(err error) error {
		writeReady(s.readyFile, readyFile{Error: err.Error()})
		return err
	}
	if err := a.paths.EnsureState(); err != nil {
		return fail(err)
	}
	logPath := filepath.Join(a.paths.LogDir(), s.id+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // our own state folder
	if err != nil {
		return fail(err)
	}
	defer logFile.Close()
	var logOut io.Writer = logFile
	if !s.printLink {
		logOut = io.MultiWriter(a.stderr, logFile)
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: slog.LevelInfo})).With("share", s.id)

	rec := store.Record{
		ID:             s.id,
		PID:            os.Getpid(),
		Socket:         control.SocketPath(a.paths.RunDir(), s.id),
		BridgeTerminal: os.Getenv("HC_TERMINAL_ID"),
		LogFile:        logPath,
	}
	var recMu sync.Mutex
	live := false
	save := func(st bridge.State) {
		recMu.Lock()
		defer recMu.Unlock()
		if !live || st.Status == bridge.StatusEnded {
			return
		}
		rec.State = st
		if err := store.Write(a.paths, rec); err != nil {
			log.Warn("could not save share record", "err", err)
		}
	}

	opts := bridge.Options{
		Terminal: s.terminal,
		Mode:     s.r.mode,
		TTL:      s.r.ttl,
		RelayURL: s.r.relay,
		Token:    s.r.token,
		Label:    s.label,
		History:  s.r.history,
		HC:       hcclient.Options{Bin: a.cfg.HC},
		Logger:   log,
		OnChange: save,
	}
	b, err := bridge.New(opts)
	if err != nil {
		return fail(err)
	}

	srv, err := control.Listen(rec.Socket, func(req control.Request) (any, error) {
		switch req.Op {
		case control.OpStatus:
		case control.OpMode:
			if err := b.SetMode(req.Mode); err != nil {
				return nil, err
			}
		case control.OpExtend:
			if req.TTLSeconds < 0 {
				return nil, errors.New("negative lifetime")
			}
			if err := b.Extend(time.Duration(req.TTLSeconds) * time.Second); err != nil {
				return nil, err
			}
			// The relay answers asynchronously; give it a moment so the
			// reply carries the new expiry.
			time.Sleep(300 * time.Millisecond)
		case control.OpStop:
			b.Stop(bridge.EndStopped)
			st := b.Snapshot()
			st.Status = bridge.StatusEnded
			st.EndReason = bridge.EndStopped
			return st, nil
		default:
			return nil, fmt.Errorf("unknown operation %q", req.Op)
		}
		return b.Snapshot(), nil
	})
	if err != nil {
		return fail(fmt.Errorf("control socket: %w", err))
	}
	defer srv.Close()
	defer store.Remove(a.paths, s.id)

	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignals()

	runErr := make(chan error, 1)
	go func() { runErr <- b.Run(ctx) }()

	select {
	case <-b.Ready():
	case err := <-runErr:
		if err == nil {
			err = errors.New("the share ended before it went live")
		}
		return fail(err)
	}
	// Ready closes on failure too: check whether Run already returned.
	select {
	case err := <-runErr:
		if err == nil {
			err = errors.New("the share ended before it went live")
		}
		return fail(err)
	default:
	}
	st := b.Snapshot()
	if st.Link == "" {
		err := <-runErr
		if err == nil {
			err = errors.New("the share did not go live")
		}
		return fail(err)
	}
	recMu.Lock()
	live = true
	rec.State = st
	werr := store.Write(a.paths, rec)
	recMu.Unlock()
	if werr != nil {
		log.Warn("could not save share record", "err", werr)
	}
	writeReady(s.readyFile, readyFile{Record: rec})
	if s.printLink {
		if err := a.reportStart(rec, s.copyLink, false); err != nil {
			log.Warn("report", "err", err)
		}
		if !a.asJSON {
			fmt.Fprintln(a.stdout, "Press Ctrl+C to stop sharing.")
		}
	}
	err = <-runErr
	final := b.Snapshot()
	log.Info("share ended", "reason", final.EndReason)
	if s.printLink && !a.asJSON {
		fmt.Fprintf(a.stdout, "Share ended (%s).\n", final.EndReason)
	}
	return err
}
