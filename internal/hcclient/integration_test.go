package hcclient

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests need a running herdr-core. Run them against an isolated session:
//
//	HERDR_SHARE_HC_TEST=1 HC_SESSION=hstest XDG_STATE_HOME=$(mktemp -d) go test ./internal/hcclient/
func needCore(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("HERDR_SHARE_HC_TEST") == "" {
		t.Skip("set HERDR_SHARE_HC_TEST=1 to run against a herdr-core")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := Dial(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func readUntil(t *testing.T, s *Stream, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var seen bytes.Buffer
	for {
		f, err := s.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v; saw %q", want, err, seen.String())
		}
		if f.Kind == KindData {
			seen.Write(f.Data)
			_ = s.Credit(len(f.Data))
			if strings.Contains(seen.String(), want) {
				return
			}
		}
	}
}

func TestAttachObserveAndType(t *testing.T) {
	c := needCore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("hcclient-test-%d", time.Now().UnixNano()%1e6)
	term, err := c.Run(ctx, RunParams{
		Argv: []string{"sh", "-c", "echo ready-$((40+2)); while IFS= read -r l; do echo \"got:$l\"; done"},
		Name: name, NoParent: true, NoNotify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Kill(context.Background(), term.Address)
		time.Sleep(200 * time.Millisecond)
		_ = c.Remove(context.Background(), term.Address)
	})

	events, snap, err := c.Subscribe(ctx, SubscribeParams{Terminal: term.Address})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 || snap[0].Address != term.Address {
		t.Fatalf("snapshot %+v", snap)
	}

	got, err := c.Get(ctx, term.Address)
	if err != nil {
		t.Fatal(err)
	}
	obs, ar, err := c.Attach(ctx, AttachParams{Terminal: term.Address, Cols: got.Cols, Rows: got.Rows, Observe: true, ResizeNotifications: true, History: 100})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("attach: resize_notifications=%v automatic_input=%v grid=%dx%d", ar.ResizeNotifications, ar.AutomaticInput, got.Cols, got.Rows)
	readUntil(t, obs, "ready-42")

	in, _, err := c.Attach(ctx, AttachParams{Terminal: term.Address, Cols: got.Cols, Rows: got.Rows})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			f, err := in.Next(ctx)
			if err != nil {
				return
			}
			if f.Kind == KindData {
				_ = in.Credit(len(f.Data))
			}
		}
	}()
	if err := in.Write([]byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, obs, "got:hello")

	after, err := c.Get(ctx, term.Address)
	if err != nil {
		t.Fatal(err)
	}
	if after.Cols != got.Cols || after.Rows != got.Rows {
		t.Fatalf("typing changed the grid: %dx%d -> %dx%d", got.Cols, got.Rows, after.Cols, after.Rows)
	}
	_ = in.Close()

	if err := c.Kill(ctx, term.Address); err != nil {
		t.Fatal(err)
	}
	for {
		ev, err := events.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for finished: %v", err)
		}
		if ev.Method == "event" && ev.Type == "finished" {
			break
		}
	}
	// The observer's stream ends with CLOSE carrying the reason.
	for {
		f, err := obs.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for close: %v", err)
		}
		if f.Kind == KindClose {
			if r := ParseClose(f.Data); r.Reason == "" {
				t.Fatalf("close without reason: %q", f.Data)
			}
			break
		}
		if f.Kind == KindData {
			_ = obs.Credit(len(f.Data))
		}
	}
}

func TestGetMissing(t *testing.T) {
	c := needCore(t)
	_, err := c.Get(context.Background(), "no-such-terminal-xyz")
	if !IsKind(err, "not_found") {
		t.Fatalf("got %v", err)
	}
}
