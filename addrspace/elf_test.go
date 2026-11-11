package addrspace

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"testing"
)

// buildMinimalELF64 synthesizes a little-endian ELF64 executable with two
// PT_LOAD segments: an R+X text segment and an R+W data segment whose BSS
// tail exceeds its file image.
func buildMinimalELF64(t *testing.T) []byte {
	t.Helper()
	const (
		ehsize    = 64
		phentsize = 56
		phnum     = 2
		phoff     = ehsize
		data1Off  = 0x100
		data2Off  = 0x180
		fileLen   = 0x1C0
	)
	var buf bytes.Buffer
	buf.Grow(fileLen)

	ident := make([]byte, 16)
	copy(ident[:4], []byte{0x7F, 'E', 'L', 'F'})
	ident[4] = 2 // ELFCLASS64
	ident[5] = 1 // ELFDATA2LSB
	ident[6] = 1 // EV_CURRENT
	buf.Write(ident)

	le := binary.LittleEndian
	mustWrite := func(v any) {
		if err := binary.Write(&buf, le, v); err != nil {
			t.Fatalf("building ELF: %v", err)
		}
	}
	mustWrite(uint16(elf.ET_EXEC))
	mustWrite(uint16(elf.EM_X86_64))
	mustWrite(uint32(1))         // e_version
	mustWrite(uint64(0x400000))  // e_entry
	mustWrite(uint64(phoff))     // e_phoff
	mustWrite(uint64(0))         // e_shoff
	mustWrite(uint32(0))         // e_flags
	mustWrite(uint16(ehsize))    // e_ehsize
	mustWrite(uint16(phentsize)) // e_phentsize
	mustWrite(uint16(phnum))     // e_phnum
	mustWrite(uint16(0))         // e_shentsize
	mustWrite(uint16(0))         // e_shnum
	mustWrite(uint16(0))         // e_shstrndx

	phdr := func(flags uint32, off, vaddr, filesz, memsz uint64) {
		mustWrite(uint32(elf.PT_LOAD))
		mustWrite(flags)
		mustWrite(off)
		mustWrite(vaddr)
		mustWrite(vaddr) // p_paddr
		mustWrite(filesz)
		mustWrite(memsz)
		mustWrite(uint64(0x1000)) // p_align
	}
	phdr(uint32(elf.PF_R|elf.PF_X), data1Off, 0x400000, 0x80, 0x80)
	phdr(uint32(elf.PF_R|elf.PF_W), data2Off, 0x401000, 0x40, 0x80)

	raw := buf.Bytes()
	if len(raw) != phoff+phnum*phentsize {
		t.Fatalf("header size = %d, want %d", len(raw), phoff+phnum*phentsize)
	}
	raw = append(raw, make([]byte, fileLen-len(raw))...)
	raw[data1Off] = 0xAB
	raw[data2Off] = 0xCD
	return raw
}

func TestELF64EndToEnd(t *testing.T) {
	raw := buildMinimalELF64(t)
	file, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("elf.NewFile: %v", err)
	}
	defer file.Close()

	segs := make([]Segment, 0, len(file.Progs))
	for _, prog := range file.Progs {
		if prog.Type != elf.PT_LOAD {
			continue
		}
		segs = append(segs, Segment{
			Off:    prog.Off,
			Vaddr:  prog.Vaddr,
			Filesz: prog.Filesz,
			Memsz:  prog.Memsz,
			Flags:  uint32(prog.Flags),
		})
	}
	if len(segs) != 2 {
		t.Fatalf("got %d PT_LOAD segments, want 2", len(segs))
	}

	space, err := New(bytes.NewReader(raw), segs)
	if err != nil {
		t.Fatalf("New from ELF: %v", err)
	}

	readOK(t, space, 0x400000, []byte{0xAB})
	readOK(t, space, 0x401000, []byte{0xCD})
	// BSS tail of the data segment: filesz 0x40, memsz 0x80.
	readOK(t, space, 0x401040, make([]byte, 0x40))
	readOK(t, space, 0x40103F, []byte{0, 0}) // file/BSS boundary

	// The two segments are separated by a gap: a spanning read must stop
	// at the first unmapped byte rather than inventing contents.
	buf := make([]byte, 0x1000)
	n, err := space.ReadAt(0x400070, buf)
	if n != 0x10 || !errors.Is(err, ErrUnmapped) {
		t.Fatalf("gap-spanning read = (%d, %v), want (16, ErrUnmapped)", n, err)
	}

	got := space.Segments()
	if len(got) != 2 || got[0].Vaddr != 0x400000 || got[1].Vaddr != 0x401000 {
		t.Fatalf("Segments() = %+v", got)
	}
}
