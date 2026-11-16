package wire

import (
	"bytes"
	"testing"
)

func TestGuestFrames(t *testing.T) {
	b := PackGuestFrame(0x01020304, []byte("hi"))
	if !bytes.Equal(b[:4], []byte{1, 2, 3, 4}) {
		t.Fatalf("header %x", b[:4])
	}
	id, p, err := UnpackGuestFrame(b)
	if err != nil || id != 0x01020304 || string(p) != "hi" {
		t.Fatalf("%d %q %v", id, p, err)
	}
	if _, _, err := UnpackGuestFrame([]byte{1}); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestPlaintext(t *testing.T) {
	kind, _, m, err := DecodePlain(EncodeApp(AppMessage{T: AppMode, Mode: ModeControl}))
	if err != nil || kind != MsgControl || m.T != AppMode || m.Mode != ModeControl {
		t.Fatalf("%v %+v %v", kind, m, err)
	}
	kind, body, _, err := DecodePlain(EncodeData(MsgInput, []byte("ls\r")))
	if err != nil || kind != MsgInput || string(body) != "ls\r" {
		t.Fatalf("%v %q %v", kind, body, err)
	}
	if _, _, _, err := DecodePlain(nil); err == nil {
		t.Fatal("empty accepted")
	}
	if _, _, _, err := DecodePlain([]byte{MsgControl, '{', '}'}); err == nil {
		t.Fatal("control without type accepted")
	}
}

func TestControl(t *testing.T) {
	c, err := ParseControl(Control{Type: TypeCreate, TTLSeconds: 60}.Marshal())
	if err != nil || c.Type != TypeCreate || c.TTLSeconds != 60 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseControl([]byte(`{}`)); err == nil {
		t.Fatal("untyped accepted")
	}
	if !ValidMode(ModeView) || !ValidMode(ModeControl) || ValidMode("write") {
		t.Fatal("modes")
	}
}
