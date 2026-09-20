package tui

import "testing"

func TestMaskKey(t *testing.T) {
	if maskKey("short") != "•••" {
		t.Fatalf("short: %q", maskKey("short"))
	}
	if got := maskKey("sk-abcdefghij1234"); got != "sk•••1234" {
		t.Fatalf("long: %q", got)
	}
}

func TestWindow(t *testing.T) {
	s, e := window(0, 20, 8)
	if s != 0 || e != 8 {
		t.Fatalf("got %d,%d", s, e)
	}
	s, e = window(19, 20, 8)
	if s != 12 || e != 20 {
		t.Fatalf("got %d,%d", s, e)
	}
	s, e = window(2, 3, 8)
	if s != 0 || e != 3 {
		t.Fatalf("got %d,%d", s, e)
	}
}

func TestConnectProviders(t *testing.T) {
	ps := connectProviders()
	if len(ps) != 4 || ps[0].ID != "ollama" {
		t.Fatalf("%+v", ps)
	}
	c := newConnect()
	if c.step != cStepProvider || len(c.provs) != 4 {
		t.Fatal("bad init")
	}
}
