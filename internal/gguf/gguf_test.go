package gguf

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wStr(b []byte, s string) []byte {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
	return append(append(b, n[:]...), []byte(s)...)
}

func wU32(b []byte, v uint32) []byte {
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], v)
	return append(b, n[:]...)
}

func wU64(b []byte, v uint64) []byte {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], v)
	return append(b, n[:]...)
}

func fixture() []byte {
	b := []byte("GGUF")
	b = wU32(b, 3)
	b = wU64(b, 2) // tensors
	b = wU64(b, 3) // kv count
	b = wStr(b, "general.architecture")
	b = wU32(b, tString)
	b = wStr(b, "llama")
	b = wStr(b, "general.quantization_version")
	b = wU32(b, tU32)
	b = wU32(b, 2)
	b = wStr(b, "general.tags")
	b = wU32(b, tArray)
	b = wU32(b, tString)
	b = wU64(b, 2)
	b = wStr(b, "chat")
	b = wStr(b, "small")
	return b
}

func TestParseFixture(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.gguf")
	extra := make([]byte, 1024)
	if err := os.WriteFile(p, append(fixture(), extra...), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 3 || info.Architecture != "llama" || info.Tensors != 2 || info.QuantVersion != 2 {
		t.Fatalf("%+v", info)
	}
	if !strings.Contains(info.Summary(), "llama") {
		t.Fatalf("summary:\n%s", info.Summary())
	}
}

func TestParseErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.gguf")
	_ = os.WriteFile(bad, []byte("NOPE........"), 0o644)
	if _, err := Parse(bad); err == nil {
		t.Fatal("expected bad-magic error")
	}
	trunc := filepath.Join(dir, "trunc.gguf")
	_ = os.WriteFile(trunc, []byte("GGUF"), 0o644)
	if _, err := Parse(trunc); err == nil {
		t.Fatal("expected truncation error")
	}
	if _, err := Parse(filepath.Join(dir, "missing.gguf")); err == nil {
		t.Fatal("expected missing-file error")
	}
}
