package safearch

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
)

// hashReader hashes exactly the bytes logically read through it. Unlike a
// tee in front of a buffered reader it never hashes readahead bytes, so the
// per-entry digests and the overall digest (read straight from the
// underlying reader) are excluded from the overall hash deterministically.
type hashReader struct {
	r io.Reader
	h hash.Hash
}

func (x *hashReader) Read(p []byte) (int, error) {
	n, err := x.r.Read(p)
	if n > 0 {
		x.h.Write(p[:n])
	}
	return n, err
}

func (x *hashReader) readByte() (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(x, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// scan parses an archive, validating every header field before acting on it.
// Each file payload is streamed through handle (never fully buffered);
// handle returns the bytes consumed and their SHA-256, which scan compares
// against the recorded digest. Any error aborts the scan.
func scan(r io.Reader, lim Limits, handle func(e Entry, body io.Reader) (int64, []byte, error)) error {
	lim = lim.withDefaults()
	raw := r
	hr := &hashReader{r: raw, h: sha256.New()}

	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(hr, magic); err != nil {
		return fmt.Errorf("%w: %v", ErrBadMagic, err)
	}
	if string(magic) != Magic {
		return fmt.Errorf("%w", ErrBadMagic)
	}
	v, err := hr.readByte()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if v != Version {
		return fmt.Errorf("%w: %d", ErrBadVersion, v)
	}

	seen := newEntrySet()
	var total int64
	count := 0
	for {
		t, err := hr.readByte()
		if err != nil {
			return fmt.Errorf("%w: truncated entry table: %v", ErrFormat, err)
		}
		if t == typEnd {
			break
		}
		count++
		if count > lim.MaxEntries || count > maxCount {
			return fmt.Errorf("%w: more than %d entries", ErrSize, lim.MaxEntries)
		}
		if t != typFile && t != typDir {
			return fmt.Errorf("%w: unknown entry type %d", ErrFormat, t)
		}

		e := Entry{Dir: t == typDir}

		var nlb [2]byte
		if _, err := io.ReadFull(hr, nlb[:]); err != nil {
			return fmt.Errorf("%w: %v", ErrFormat, err)
		}
		nameLen := int(binary.LittleEndian.Uint16(nlb[:]))
		if nameLen == 0 || nameLen > maxNameLen {
			return fmt.Errorf("%w: name length %d", ErrFormat, nameLen)
		}
		// Length validated above; only now allocate.
		nameBuf := make([]byte, nameLen)
		if _, err := io.ReadFull(hr, nameBuf); err != nil {
			return fmt.Errorf("%w: %v", ErrFormat, err)
		}
		name, err := ValidatePath(string(nameBuf))
		if err != nil {
			return err
		}
		e.Name = name

		if t == typFile {
			var fixed [12]byte // mode u32 + size u64
			if _, err := io.ReadFull(hr, fixed[:]); err != nil {
				return fmt.Errorf("%w: %v", ErrFormat, err)
			}
			mode := binary.LittleEndian.Uint32(fixed[:4])
			if mode&^0o777 != 0 {
				return fmt.Errorf("%w: mode %#o has non-permission bits", ErrFormat, mode)
			}
			e.Mode = fs.FileMode(mode)
			e.Size = int64(binary.LittleEndian.Uint64(fixed[4:]))
			if e.Size < 0 || e.Size > lim.MaxFileSize {
				return fmt.Errorf("%w: %q is %d bytes (limit %d)", ErrSize, e.Name, e.Size, lim.MaxFileSize)
			}
			total += e.Size
			if total > lim.MaxTotalSize {
				return fmt.Errorf("%w: total payload exceeds %d bytes", ErrSize, lim.MaxTotalSize)
			}
		} else {
			var mb [4]byte
			if _, err := io.ReadFull(hr, mb[:]); err != nil {
				return fmt.Errorf("%w: %v", ErrFormat, err)
			}
			mode := binary.LittleEndian.Uint32(mb[:])
			if mode&^0o777 != 0 {
				return fmt.Errorf("%w: mode %#o has non-permission bits", ErrFormat, mode)
			}
			e.Mode = fs.FileMode(mode)
		}

		if err := seen.add(e.Name, e.Dir); err != nil {
			return err
		}

		n, got, err := handle(e, io.LimitReader(hr, e.Size))
		if err != nil {
			return err
		}
		if n != e.Size {
			return fmt.Errorf("%w: %q payload truncated: got %d of %d bytes", ErrFormat, e.Name, n, e.Size)
		}
		if t == typFile {
			// The per-entry digest follows the payload and is read from the
			// raw stream: it is not part of the overall hash.
			want := make([]byte, digestLen)
			if _, err := io.ReadFull(raw, want); err != nil {
				return fmt.Errorf("%w: %v", ErrFormat, err)
			}
			if !bytes.Equal(got, want) {
				return fmt.Errorf("%w: %q", ErrDigest, e.Name)
			}
		}
	}

	wantOverall := make([]byte, digestLen)
	if _, err := io.ReadFull(raw, wantOverall); err != nil {
		return fmt.Errorf("%w: missing overall digest: %v", ErrFormat, err)
	}
	if !bytes.Equal(hr.h.Sum(nil), wantOverall) {
		return fmt.Errorf("%w: overall", ErrDigest)
	}
	var tail [1]byte
	switch n, err := raw.Read(tail[:]); {
	case n == 0 && errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return fmt.Errorf("%w: trailing byte %#x after archive", ErrFormat, tail[0])
	default:
		return fmt.Errorf("%w: %v", ErrFormat, err)
	}
}

// List returns the entries of the archive in r without writing anything to
// disk. Payloads are still streamed and checksummed.
func List(r io.Reader, lim Limits) ([]Entry, error) {
	var out []Entry
	err := scan(r, lim, func(e Entry, body io.Reader) (int64, []byte, error) {
		out = append(out, e)
		h := sha256.New()
		n, err := io.Copy(h, body)
		return n, h.Sum(nil), err
	})
	return out, err
}
