package e2ee

import (
	"bytes"
	"errors"
	"testing"
)

func pair(t *testing.T) (*Channel, *Channel) {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	cid, err := NewCID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewChannel(k, cid, ToGuest)
	if err != nil {
		t.Fatal(err)
	}
	g, err := NewChannel(k, cid, ToBridge)
	if err != nil {
		t.Fatal(err)
	}
	return b, g
}

func TestRoundTrip(t *testing.T) {
	b, g := pair(t)
	for _, msg := range [][]byte{[]byte("hello"), {}, bytes.Repeat([]byte{7}, 1<<16)} {
		f, err := b.Seal(msg)
		if err != nil {
			t.Fatal(err)
		}
		got, err := g.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("got %d bytes, want %d", len(got), len(msg))
		}
		f, _ = g.Seal(msg)
		if got, err = b.Open(f); err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("reverse direction: %v", err)
		}
	}
}

func TestRejectsReplayAndReflection(t *testing.T) {
	b, g := pair(t)
	f, _ := b.Seal([]byte("one"))
	if _, err := g.Open(f); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Open(f); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: got %v", err)
	}
	// A frame reflected back to its sealer must fail: direction is bound.
	f2, _ := b.Seal([]byte("two"))
	if _, err := b.Open(f2); !errors.Is(err, ErrAuth) {
		t.Fatalf("reflection: got %v", err)
	}
}

func TestRejectsOtherConnectionAndTampering(t *testing.T) {
	b, _ := pair(t)
	k := Key{}
	cid := CID{}
	other, _ := NewChannel(k, cid, ToBridge)
	f, _ := b.Seal([]byte("secret"))
	if _, err := other.Open(f); !errors.Is(err, ErrAuth) {
		t.Fatalf("other key: got %v", err)
	}
	b2, g2 := pair(t)
	f, _ = b2.Seal([]byte("secret"))
	f[len(f)-1] ^= 1
	if _, err := g2.Open(f); !errors.Is(err, ErrAuth) {
		t.Fatalf("tamper: got %v", err)
	}
	if _, err := g2.Open([]byte{1, 2}); !errors.Is(err, ErrAuth) {
		t.Fatalf("short: got %v", err)
	}
}

func TestKeyEncoding(t *testing.T) {
	k, _ := NewKey()
	s := k.String()
	if len(s) != 43 {
		t.Fatalf("encoded length %d", len(s))
	}
	back, err := ParseKey(s)
	if err != nil || back != k {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := ParseKey("abc"); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := ParseKey("!!!"); err == nil {
		t.Fatal("bad encoding accepted")
	}
}

// A fixed vector shared with the browser implementation (web/src/crypto.test.ts).
func TestKnownVector(t *testing.T) {
	var k Key
	for i := range k {
		k[i] = byte(i)
	}
	var cid CID
	for i := range cid {
		cid[i] = byte(0xA0 + i)
	}
	frame := []byte{
		0, 0, 0, 0, 0, 0, 0, 1, // seq
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, // nonce
	}
	ch, _ := NewChannel(k, cid, ToBridge)
	sealed := ch.aead.Seal(append([]byte{}, frame...), frame[8:20], []byte("hi"), ad(ToGuest, cid, 1))
	got, err := ch.Open(sealed)
	if err != nil || string(got) != "hi" {
		t.Fatalf("vector: %v %q", err, got)
	}
	const want = "000000000000000100010203040506070809" + "0a0b"
	if hexs(sealed[:20]) != want {
		t.Fatalf("header %s", hexs(sealed[:20]))
	}
	if VectorHex != hexs(sealed) {
		t.Fatalf("vector changed: %s", hexs(sealed))
	}
}

const VectorHex = "0000000000000001000102030405060708090a0b2f6b4370a728c2d292f1ec0e09f2c9d9a40e"

func hexs(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}
