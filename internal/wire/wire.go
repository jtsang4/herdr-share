// Package wire defines the messages exchanged with the relay and, inside the
// end-to-end encrypted envelope, between a bridge and its guests.
//
// See docs/protocol.md for the full description.
package wire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
)

// Relay endpoints, relative to the relay's base URL.
const (
	HostPath  = "/api/v1/host"
	GuestPath = "/api/v1/shares/" // + {id} + "/guest"
	InfoPath  = "/api/v1/info"
	SharePage = "/s/" // + {id}
)

// WebSocket close codes used by the relay (4000-4999 are application codes).
const (
	CloseEnded        = 4000 // the share ended; see the "ended" message for why
	CloseNotFound     = 4004 // no such share
	CloseUnauthorized = 4001
	CloseTooSlow      = 4008 // the guest fell too far behind; reconnect for a fresh screen
	CloseFull         = 4009 // the share has its maximum number of guests
	CloseKicked       = 4010
	CloseProtocol     = 4002
	CloseReplaced     = 4011 // the host resumed on another connection
)

// Message types on the relay control plane (WebSocket text messages).
const (
	// host -> relay
	TypeCreate = "create"
	TypeResume = "resume"
	TypeKick   = "kick"
	TypeClose  = "close"
	TypeExtend = "extend"
	// relay -> host
	TypeCreated   = "created"
	TypeResumed   = "resumed"
	TypeGuestJoin = "guest_join"
	TypeGuestLeft = "guest_left"
	TypeExpired   = "expired"
	TypeExtended  = "extended"
	TypeError     = "error"
	// relay -> guest
	TypeJoined      = "joined"
	TypeHostOffline = "host_offline"
	TypeHostOnline  = "host_online"
	TypeEnded       = "ended"
)

// Reasons a share ended, sent to guests in cleartext by the relay.
const (
	ReasonStopped  = "stopped"
	ReasonExpired  = "expired"
	ReasonNotFound = "not_found"
	ReasonHostLost = "host_lost"
	ReasonKicked   = "kicked"
	ReasonShutdown = "shutdown"
)

// Control is the envelope for every relay control message. Unused fields are
// omitted; Type decides which ones matter.
type Control struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol,omitempty"`
	Client   string `json:"client,omitempty"`

	// create / resume
	Token      string `json:"token,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
	ShareID    string `json:"share_id,omitempty"`
	Secret     string `json:"secret,omitempty"`

	// created / resumed / extended
	GuestBase string `json:"guest_base,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // unix seconds; 0 = never
	MaxGuests int    `json:"max_guests,omitempty"`

	// guest_join / guest_left / kick / joined
	Guest uint32 `json:"guest,omitempty"`
	Addr  string `json:"addr,omitempty"`

	// error / ended / close / kick
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Marshal encodes a control message.
func (c Control) Marshal() []byte {
	b, _ := json.Marshal(c) //nolint:gosec // the share secret is meant to travel in the create reply
	return b
}

// ParseControl decodes a control message.
func ParseControl(b []byte) (Control, error) {
	var c Control
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Type == "" {
		return c, errors.New("control message without type")
	}
	return c, nil
}

// Host binary frames carry a guest id in front of an opaque payload.

// PackGuestFrame prefixes payload with the guest id.
func PackGuestFrame(guest uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, guest)
	copy(out[4:], payload)
	return out
}

// UnpackGuestFrame splits a host frame into guest id and payload.
func UnpackGuestFrame(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, errors.New("short host frame")
	}
	return binary.BigEndian.Uint32(b), b[4:], nil
}
