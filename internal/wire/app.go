package wire

import (
	"encoding/json"
	"errors"
)

// Outer framing of a guest payload, as forwarded by the relay.
const (
	// EnvInit opens a connection: cid [16] followed by a sealed frame.
	EnvInit byte = 'I'
	// EnvFrame is a sealed frame.
	EnvFrame byte = 'F'
)

// Plaintext message kinds inside sealed frames.
const (
	MsgControl byte = 1 // JSON AppMessage
	MsgOutput  byte = 2 // terminal output bytes (bridge -> guest)
	MsgInput   byte = 3 // keyboard input bytes (guest -> bridge)
)

// Share modes.
const (
	ModeView    = "view"    // guests watch only
	ModeControl = "control" // guests may type
)

// ValidMode reports whether m is a known mode.
func ValidMode(m string) bool { return m == ModeView || m == ModeControl }

// App message types (field T).
const (
	AppInit    = "init"    // bridge: first sealed message of a connection
	AppHello   = "hello"   // guest: who is joining
	AppWelcome = "welcome" // bridge: share facts; output follows
	AppResize  = "resize"  // bridge: the terminal grid changed; apply before later output
	AppMode    = "mode"    // bridge: the share's mode changed
	AppTitle   = "title"   // bridge: the terminal title changed
	AppExpires = "expires" // bridge: the share's expiry changed
	AppViewers = "viewers" // bridge: how many guests are connected
	AppReset   = "reset"   // bridge: clear the screen; a fresh paint follows
	AppEnd     = "end"     // bridge: the share is over
)

// AppMessage is the JSON control message exchanged end to end.
type AppMessage struct {
	T string `json:"t"`

	// welcome / mode
	Mode      string `json:"mode,omitempty"`
	Title     string `json:"title,omitempty"`
	Label     string `json:"label,omitempty"`
	Terminal  string `json:"terminal,omitempty"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Viewers   int    `json:"viewers,omitempty"`
	Version   string `json:"version,omitempty"`

	// hello
	Name   string `json:"name,omitempty"`
	Client string `json:"client,omitempty"`

	// end
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// EncodeApp encodes a control message as plaintext.
func EncodeApp(m AppMessage) []byte {
	b, _ := json.Marshal(m)
	return append([]byte{MsgControl}, b...)
}

// EncodeData encodes output or input bytes as plaintext.
func EncodeData(kind byte, data []byte) []byte {
	out := make([]byte, 1+len(data))
	out[0] = kind
	copy(out[1:], data)
	return out
}

// DecodePlain splits plaintext into its kind and body; for MsgControl the
// message is decoded too.
func DecodePlain(pt []byte) (kind byte, body []byte, msg AppMessage, err error) {
	if len(pt) == 0 {
		return 0, nil, msg, errors.New("empty message")
	}
	kind, body = pt[0], pt[1:]
	if kind == MsgControl {
		if err = json.Unmarshal(body, &msg); err == nil && msg.T == "" {
			err = errors.New("control message without type")
		}
	}
	return kind, body, msg, err
}
