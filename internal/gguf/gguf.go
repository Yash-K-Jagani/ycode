package gguf

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// Value types in GGUF metadata (spec).
const (
	tU8 = iota
	tI8
	tU16
	tI16
	tU32
	tI32
	tF32
	tBool
	tString
	tArray
	tU64
	tI64
	tF64
)

type Info struct {
	Path         string
	Version      uint32
	Architecture string
	Name         string
	SizeLabel    string
	QuantVersion uint32
	Tensors      uint64
	Params       uint64 // estimated from tensor shapes when readable
	Bytes        int64
}

// Parse reads the GGUF header + metadata key-values (tensor data skipped).
func Parse(path string) (Info, error) {
	var info Info
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return info, err
	}
	info.Path = path
	info.Bytes = fi.Size()
	r := &reader{r: f}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r.r, magic); err != nil {
		return info, fmt.Errorf("not a gguf file: %w", err)
	}
	if string(magic) != "GGUF" {
		return info, fmt.Errorf("bad magic %q (want GGUF)", magic)
	}
	info.Version, _ = r.u32()
	if info.Version != 3 {
		return info, fmt.Errorf("unsupported gguf version %d (want 3)", info.Version)
	}
	info.Tensors, _ = r.u64()
	nKV, _ := r.u64()
	if err := r.err; err != nil {
		return info, fmt.Errorf("truncated header: %w", err)
	}
	meta := map[string]string{}
	for i := uint64(0); i < nKV; i++ {
		k, err := r.str()
		if err != nil {
			return info, fmt.Errorf("metadata key: %w", err)
		}
		v, err := r.value()
		if err != nil {
			return info, fmt.Errorf("metadata %q: %w", k, err)
		}
		meta[k] = v
		if r.err != nil {
			return info, fmt.Errorf("metadata: %w", r.err)
		}
	}
	info.Architecture = meta["general.architecture"]
	info.Name = meta["general.name"]
	info.SizeLabel = meta["general.size_label"]
	if qv, ok := meta["general.quantization_version"]; ok {
		var n uint64
		_, _ = fmt.Sscanf(qv, "%d", &n)
		info.QuantVersion = uint32(n)
	}
	return info, nil
}

type reader struct {
	r   io.Reader
	err error
}

func (r *reader) u32() (uint32, error) {
	var v uint32
	if r.err != nil {
		return 0, r.err
	}
	r.err = binary.Read(r.r, binary.LittleEndian, &v)
	return v, r.err
}

func (r *reader) u64() (uint64, error) {
	var v uint64
	if r.err != nil {
		return 0, r.err
	}
	r.err = binary.Read(r.r, binary.LittleEndian, &v)
	return v, r.err
}

func (r *reader) str() (string, error) {
	n, err := r.u64()
	if err != nil {
		return "", err
	}
	if n > 1<<20 {
		r.err = fmt.Errorf("string too long: %d", n)
		return "", r.err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r.r, b); err != nil {
		r.err = err
		return "", err
	}
	return string(b), nil
}

func (r *reader) value() (string, error) {
	t, err := r.u32()
	if err != nil {
		return "", err
	}
	switch t {
	case tU8:
		var v uint8
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tI8:
		var v int8
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tU16:
		var v uint16
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tI16:
		var v int16
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tU32:
		v, err := r.u32()
		return fmt.Sprintf("%d", v), err
	case tI32:
		var v int32
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tF32:
		var v float32
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return strings.TrimRight(fmt.Sprintf("%f", v), "0"), r.err
	case tBool:
		var v uint8
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%v", v != 0), r.err
	case tString:
		return r.str()
	case tU64:
		v, err := r.u64()
		return fmt.Sprintf("%d", v), err
	case tI64:
		var v int64
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%d", v), r.err
	case tF64:
		var v float64
		r.err = binary.Read(r.r, binary.LittleEndian, &v)
		return fmt.Sprintf("%v", v), r.err
	case tArray:
		et, err := r.u32()
		if err != nil {
			return "", err
		}
		n, err := r.u64()
		if err != nil {
			return "", err
		}
		if n > 1<<16 {
			return "", fmt.Errorf("array too long: %d", n)
		}
		var parts []string
		for i := uint64(0); i < n; i++ {
			var s string
			switch et {
			case tString:
				s, err = r.str()
			case tU32:
				var v uint32
				v, err = r.u32()
				s = fmt.Sprintf("%d", v)
			case tU64:
				var v uint64
				v, err = r.u64()
				s = fmt.Sprintf("%d", v)
			default:
				return "", fmt.Errorf("unsupported array elem type %d", et)
			}
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, ", "), nil
	default:
		return "", fmt.Errorf("unknown value type %d", t)
	}
}

// Summary renders a one-screen description.
func (i Info) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", i.Path)
	fmt.Fprintf(&b, "format: GGUFv%d · arch: %s · tensors: %d\n", i.Version, orDash(i.Architecture), i.Tensors)
	fmt.Fprintf(&b, "name: %s · size: %s · quant-version: %d\n", orDash(i.Name), orDash(i.SizeLabel), i.QuantVersion)
	fmt.Fprintf(&b, "file: %.1f GB", float64(i.Bytes)/1e9)
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
