// Package e2ee encrypts everything that flows between a sharer's bridge and a
// guest's browser, so the relay only ever forwards opaque bytes.
//
// The share key (32 random bytes) lives in the share link's URL fragment,
// which browsers never send to servers. Each guest connection gets a fresh
// random connection id (cid) chosen by the bridge. Every sealed frame is bound
// to its direction, its cid and a strictly increasing sequence number through
// AES-256-GCM additional data, so a relay can neither read, alter, reorder,
// nor replay frames, not even into another connection.
//
// Sealed frame layout:
//
//	seq u64 BE | nonce [12] | AES-256-GCM(ciphertext || tag)
//
// Additional data: "herdr-share/1" | dir u8 | cid [16] | seq u64 BE.
package e2ee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// KeySize is the share key length in bytes.
const KeySize = 32

// CIDSize is the connection id length in bytes.
const CIDSize = 16

const (
	seqSize   = 8
	nonceSize = 12
	// Overhead is how many bytes sealing adds to a plaintext.
	Overhead = seqSize + nonceSize + 16
)

var label = []byte("herdr-share/1")

// Direction says who sealed a frame.
type Direction byte

const (
	// ToGuest frames are sealed by the bridge.
	ToGuest Direction = 'B'
	// ToBridge frames are sealed by a guest.
	ToBridge Direction = 'G'
)

// Key is a share key.
type Key [KeySize]byte

// CID identifies one guest connection.
type CID [CIDSize]byte

// NewKey returns a random key.
func NewKey() (Key, error) {
	var k Key
	_, err := rand.Read(k[:])
	return k, err
}

// NewCID returns a random connection id.
func NewCID() (CID, error) {
	var c CID
	_, err := rand.Read(c[:])
	return c, err
}

// String encodes the key as unpadded base64url, the form used in links.
func (k Key) String() string { return base64.RawURLEncoding.EncodeToString(k[:]) }

// ParseKey decodes a key produced by Key.String.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return k, fmt.Errorf("invalid key encoding: %w", err)
	}
	if len(b) != KeySize {
		return k, fmt.Errorf("invalid key length %d", len(b))
	}
	copy(k[:], b)
	return k, nil
}

// ErrAuth means a frame failed authentication: wrong key, tampering, or a
// frame from another connection.
var ErrAuth = errors.New("e2ee: frame failed authentication")

// ErrReplay means a frame's sequence number did not increase.
var ErrReplay = errors.New("e2ee: frame replayed or out of order")

// Channel seals frames in one direction and opens frames from the other,
// for one connection. It is safe for concurrent use; sequence numbers follow
// the order in which Seal calls complete.
type Channel struct {
	aead    cipher.AEAD
	cid     CID
	send    Direction
	recv    Direction
	sendMu  sync.Mutex
	sendSeq uint64
	recvMu  sync.Mutex
	recvSeq uint64
}

// NewChannel builds a channel that seals in direction send.
func NewChannel(k Key, cid CID, send Direction) (*Channel, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	recv := ToBridge
	if send == ToBridge {
		recv = ToGuest
	}
	return &Channel{aead: aead, cid: cid, send: send, recv: recv}, nil
}

// CID returns the connection id.
func (c *Channel) CID() CID { return c.cid }

func ad(dir Direction, cid CID, seq uint64) []byte {
	out := make([]byte, 0, len(label)+1+CIDSize+seqSize)
	out = append(out, label...)
	out = append(out, byte(dir))
	out = append(out, cid[:]...)
	return binary.BigEndian.AppendUint64(out, seq)
}

// Seal encrypts plaintext as the next frame.
func (c *Channel) Seal(plaintext []byte) ([]byte, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.sendSeq++
	seq := c.sendSeq
	out := make([]byte, seqSize+nonceSize, seqSize+nonceSize+len(plaintext)+c.aead.Overhead())
	binary.BigEndian.PutUint64(out, seq)
	if _, err := rand.Read(out[seqSize:]); err != nil {
		return nil, err
	}
	nonce := out[seqSize : seqSize+nonceSize]
	return c.aead.Seal(out, nonce, plaintext, ad(c.send, c.cid, seq)), nil
}

// Open authenticates and decrypts the next frame from the peer.
func (c *Channel) Open(frame []byte) ([]byte, error) {
	if len(frame) < Overhead {
		return nil, ErrAuth
	}
	seq := binary.BigEndian.Uint64(frame)
	nonce := frame[seqSize : seqSize+nonceSize]
	pt, err := c.aead.Open(nil, nonce, frame[seqSize+nonceSize:], ad(c.recv, c.cid, seq))
	if err != nil {
		return nil, ErrAuth
	}
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	if seq <= c.recvSeq {
		return nil, ErrReplay
	}
	c.recvSeq = seq
	return pt, nil
}
