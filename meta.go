package netsec

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
)

const maxDynamicRead = 64 << 20

// ReadELF reads the dynamic tags that affect library lookup. It uses the
// section form when SHT_DYNAMIC is present and falls back to PT_DYNAMIC for
// section-stripped binaries.
func ReadELF(path string) (ELFMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return ELFMetadata{}, err
	}
	defer file.Close()

	f, err := elf.NewFile(file)
	if err != nil {
		return ELFMetadata{}, err
	}

	for _, section := range f.Sections {
		if section.Type == elf.SHT_DYNAMIC {
			return readELFSections(f)
		}
	}
	return readELFSegments(file, f)
}

// ReadELFFile reads dynamic tags from an already opened ELF file using its
// section headers. Use ReadELF when the file may have no section headers.
func ReadELFFile(f *elf.File) (ELFMetadata, error) {
	if f == nil {
		return ELFMetadata{}, errors.New("netsec: nil ELF file")
	}
	return readELFSections(f)
}

func readELFSections(f *elf.File) (ELFMetadata, error) {
	needed, err := f.DynString(elf.DT_NEEDED)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_NEEDED: %w", err)
	}
	rpath, err := f.DynString(elf.DT_RPATH)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_RPATH: %w", err)
	}
	runpath, err := f.DynString(elf.DT_RUNPATH)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_RUNPATH: %w", err)
	}
	rpathTags, err := f.DynValue(elf.DT_RPATH)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_RPATH tag: %w", err)
	}
	runpathTags, err := f.DynValue(elf.DT_RUNPATH)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_RUNPATH tag: %w", err)
	}
	flags1, err := f.DynValue(elf.DT_FLAGS_1)
	if err != nil {
		return ELFMetadata{}, fmt.Errorf("read DT_FLAGS_1: %w", err)
	}

	meta := ELFMetadata{
		Needed:     needed,
		RPATH:      rpath,
		RUNPATH:    runpath,
		HasRPATH:   len(rpathTags) > 0,
		HasRUNPATH: len(runpathTags) > 0,
	}
	for _, value := range flags1 {
		if value&uint64(elf.DF_1_NODEFLIB) != 0 {
			meta.NoDefaultLibs = true
			break
		}
	}
	return meta, nil
}

type dynamicEntry struct {
	tag elf.DynTag
	val uint64
}

func readELFSegments(reader io.ReaderAt, f *elf.File) (ELFMetadata, error) {
	var dynamic *elf.Prog
	for _, prog := range f.Progs {
		if prog.Type == elf.PT_DYNAMIC {
			dynamic = prog
			break
		}
	}
	if dynamic == nil {
		return ELFMetadata{}, nil
	}
	if dynamic.Filesz > maxDynamicRead {
		return ELFMetadata{}, fmt.Errorf("netsec: PT_DYNAMIC is too large (%d bytes)", dynamic.Filesz)
	}

	raw := make([]byte, dynamic.Filesz)
	if _, err := io.ReadFull(io.NewSectionReader(reader, int64(dynamic.Off), int64(dynamic.Filesz)), raw); err != nil {
		return ELFMetadata{}, fmt.Errorf("read PT_DYNAMIC: %w", err)
	}

	entrySize := 8
	if f.Class == elf.ELFCLASS64 {
		entrySize = 16
	}
	if len(raw) < entrySize {
		return ELFMetadata{}, errors.New("netsec: empty PT_DYNAMIC segment")
	}

	entries := make([]dynamicEntry, 0, len(raw)/entrySize)
	for len(raw) >= entrySize {
		var tag elf.DynTag
		var value uint64
		switch f.Class {
		case elf.ELFCLASS32:
			tag = elf.DynTag(f.ByteOrder.Uint32(raw[0:4]))
			value = uint64(f.ByteOrder.Uint32(raw[4:8]))
		case elf.ELFCLASS64:
			tag = elf.DynTag(f.ByteOrder.Uint64(raw[0:8]))
			value = f.ByteOrder.Uint64(raw[8:16])
		default:
			return ELFMetadata{}, fmt.Errorf("netsec: unsupported ELF class %v", f.Class)
		}
		raw = raw[entrySize:]
		if tag == elf.DT_NULL {
			break
		}
		entries = append(entries, dynamicEntry{tag: tag, val: value})
	}

	var strtabAddr, strtabSize uint64
	for _, entry := range entries {
		switch entry.tag {
		case elf.DT_STRTAB:
			strtabAddr = entry.val
		case elf.DT_STRSZ:
			strtabSize = entry.val
		}
	}
	if strtabAddr == 0 || strtabSize == 0 {
		return ELFMetadata{}, errors.New("netsec: PT_DYNAMIC has no usable string table")
	}
	if strtabSize > maxDynamicRead {
		return ELFMetadata{}, fmt.Errorf("netsec: dynamic string table is too large (%d bytes)", strtabSize)
	}

	offset, ok := virtualAddressOffset(f, strtabAddr)
	if !ok {
		return ELFMetadata{}, errors.New("netsec: dynamic string table is not file-backed")
	}
	strtab := make([]byte, strtabSize)
	if _, err := io.ReadFull(io.NewSectionReader(reader, int64(offset), int64(strtabSize)), strtab); err != nil {
		return ELFMetadata{}, fmt.Errorf("read dynamic string table: %w", err)
	}

	var meta ELFMetadata
	for _, entry := range entries {
		switch entry.tag {
		case elf.DT_NEEDED:
			name, err := dynamicString(strtab, entry.val)
			if err != nil {
				return ELFMetadata{}, fmt.Errorf("DT_NEEDED: %w", err)
			}
			meta.Needed = append(meta.Needed, name)
		case elf.DT_RPATH:
			meta.HasRPATH = true
			value, err := dynamicString(strtab, entry.val)
			if err != nil {
				return ELFMetadata{}, fmt.Errorf("DT_RPATH: %w", err)
			}
			meta.RPATH = append(meta.RPATH, value)
		case elf.DT_RUNPATH:
			meta.HasRUNPATH = true
			value, err := dynamicString(strtab, entry.val)
			if err != nil {
				return ELFMetadata{}, fmt.Errorf("DT_RUNPATH: %w", err)
			}
			meta.RUNPATH = append(meta.RUNPATH, value)
		case elf.DT_FLAGS_1:
			if entry.val&uint64(elf.DF_1_NODEFLIB) != 0 {
				meta.NoDefaultLibs = true
			}
		}
	}
	return meta, nil
}

func virtualAddressOffset(f *elf.File, address uint64) (uint64, bool) {
	for _, prog := range f.Progs {
		if prog.Type != elf.PT_LOAD || address < prog.Vaddr {
			continue
		}
		delta := address - prog.Vaddr
		if delta < prog.Filesz {
			return prog.Off + delta, true
		}
	}
	return 0, false
}

func dynamicString(table []byte, offset uint64) (string, error) {
	if offset >= uint64(len(table)) {
		return "", fmt.Errorf("string offset %d outside table of %d bytes", offset, len(table))
	}
	start := int(offset)
	end := start
	for end < len(table) && table[end] != 0 {
		end++
	}
	if end == len(table) {
		return "", errors.New("unterminated dynamic string")
	}
	return string(table[start:end]), nil
}
