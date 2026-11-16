// Package store keeps one JSON record per running share in the state
// folder, so the CLI and the Herdr mod can list and control shares.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jtsang4/herdr-share/internal/bridge"
	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/control"
)

// Record is a running share as seen from outside its bridge. It contains
// the share link (and so the key): the file is private to the user.
type Record struct {
	ID             string    `json:"id"`
	PID            int       `json:"pid"`
	Socket         string    `json:"socket"`
	BridgeTerminal string    `json:"bridge_terminal,omitempty"`
	LogFile        string    `json:"log_file,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	bridge.State
}

func path(p config.Paths, id string) string {
	return filepath.Join(p.SharesDir(), id+".json")
}

// Write saves a record atomically.
func Write(p config.Paths, r Record) error {
	if err := p.EnsureState(); err != nil {
		return err
	}
	r.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path(p, r.ID), append(b, '\n'), 0o600)
}

// Remove deletes a record.
func Remove(p config.Paths, id string) {
	_ = os.Remove(path(p, id))
}

// List returns every record, newest first.
func List(p config.Paths) ([]Record, error) {
	entries, err := os.ReadDir(p.SharesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(p.SharesDir(), e.Name()))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) == nil && r.ID != "" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Alive asks the record's bridge for its status. A bridge that does not
// answer is gone; its record is stale.
func Alive(r Record) (bridge.State, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := control.Call(ctx, r.Socket, control.Request{Op: control.OpStatus})
	if err != nil {
		return bridge.State{}, false
	}
	var s bridge.State
	if json.Unmarshal(resp.State, &s) != nil {
		return bridge.State{}, false
	}
	return s, true
}

// Live returns the records whose bridges answer, refreshed with their
// current state, and removes stale ones.
func Live(p config.Paths) ([]Record, error) {
	all, err := List(p)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range all {
		s, ok := Alive(r)
		if !ok {
			Remove(p, r.ID)
			continue
		}
		r.State = s
		out = append(out, r)
	}
	return out, nil
}

// ErrNotFound means no running share matches a reference.
var ErrNotFound = errors.New("no running share matches")

// Find resolves a reference: a share's local id, its relay share id (or a
// unique prefix of either), or the terminal it shares.
func Find(p config.Paths, ref string) (Record, error) {
	live, err := Live(p)
	if err != nil {
		return Record{}, err
	}
	var matches []Record
	for _, r := range live {
		if r.ID == ref || r.ShareID == ref || r.Terminal == ref {
			return r, nil
		}
	}
	for _, r := range live {
		if strings.HasPrefix(r.ID, ref) || (r.ShareID != "" && strings.HasPrefix(r.ShareID, ref)) {
			matches = append(matches, r)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Record{}, errors.New("ambiguous share reference " + ref)
	}
	return Record{}, ErrNotFound
}
