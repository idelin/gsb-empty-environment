package netsec

import (
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

type testELFSpec struct {
	Needed     []string
	RPATH      string
	HasRPATH   bool
	RUNPATH    string
	HasRUNPATH bool
	Flags1     uint64
}

func writeTestELF(t *testing.T, spec testELFSpec) string {
	t.Helper()

	dynamicStrings := []byte{0}
	stringOffset := make(map[string]uint64)
	addString := func(value string) uint64 {
		if offset, ok := stringOffset[value]; ok {
			return offset
		}
		offset := uint64(len(dynamicStrings))
		stringOffset[value] = offset
		dynamicStrings = append(dynamicStrings, value...)
		dynamicStrings = append(dynamicStrings, 0)
		return offset
	}

	var dynamicTags []elf.DynTag
	var dynamicValues []uint64
	addTag := func(tag elf.DynTag, value uint64) {
		dynamicTags = append(dynamicTags, tag)
		dynamicValues = append(dynamicValues, value)
	}
	for _, needed := range spec.Needed {
		addTag(elf.DT_NEEDED, addString(needed))
	}
	if spec.HasRPATH {
		addTag(elf.DT_RPATH, addString(spec.RPATH))
	}
	if spec.HasRUNPATH {
		addTag(elf.DT_RUNPATH, addString(spec.RUNPATH))
	}
	if spec.Flags1 != 0 {
		addTag(elf.DT_FLAGS_1, spec.Flags1)
	}
	addTag(elf.DT_NULL, 0)

	shstr := []byte{0}
	sectionName := func(value string) uint32 {
		offset := uint32(len(shstr))
		shstr = append(shstr, value...)
		shstr = append(shstr, 0)
		return offset
	}
	dynstrName := sectionName(".dynstr")
	dynamicName := sectionName(".dynamic")
	shstrName := sectionName(".shstrtab")

	const headerSize = 64
	const sectionHeaderSize = 64
	const sectionCount = 4
	align := func(value, alignment int) int {
		return (value + alignment - 1) &^ (alignment - 1)
	}

	dynstrOffset := headerSize
	dynamicOffset := align(dynstrOffset+len(dynamicStrings), 16)
	dynamicData := make([]byte, len(dynamicTags)*16)
	for i := range dynamicTags {
		binary.LittleEndian.PutUint64(dynamicData[i*16:], uint64(dynamicTags[i]))
		binary.LittleEndian.PutUint64(dynamicData[i*16+8:], dynamicValues[i])
	}
	shstrOffset := dynamicOffset + len(dynamicData)
	sectionHeadersOffset := align(shstrOffset+len(shstr), 16)
	fileSize := sectionHeadersOffset + sectionCount*sectionHeaderSize
	data := make([]byte, fileSize)

	copy(data[0:4], []byte{0x7f, 'E', 'L', 'F'})
	data[4] = byte(elf.ELFCLASS64)
	data[5] = byte(elf.ELFDATA2LSB)
	data[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(data[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint64(data[40:], uint64(sectionHeadersOffset))
	binary.LittleEndian.PutUint16(data[52:], headerSize)
	binary.LittleEndian.PutUint16(data[54:], 56)
	binary.LittleEndian.PutUint16(data[58:], sectionHeaderSize)
	binary.LittleEndian.PutUint16(data[60:], sectionCount)
	binary.LittleEndian.PutUint16(data[62:], 3)

	copy(data[dynstrOffset:], dynamicStrings)
	copy(data[dynamicOffset:], dynamicData)
	copy(data[shstrOffset:], shstr)

	writeSection := func(index int, name uint32, typ elf.SectionType, flags elf.SectionFlag, offset, size int, link uint32, info uint32, alignment, entsize uint64) {
		base := sectionHeadersOffset + index*sectionHeaderSize
		binary.LittleEndian.PutUint32(data[base:], name)
		binary.LittleEndian.PutUint32(data[base+4:], uint32(typ))
		binary.LittleEndian.PutUint64(data[base+8:], uint64(flags))
		binary.LittleEndian.PutUint64(data[base+16:], 0)
		binary.LittleEndian.PutUint64(data[base+24:], uint64(offset))
		binary.LittleEndian.PutUint64(data[base+32:], uint64(size))
		binary.LittleEndian.PutUint32(data[base+40:], link)
		binary.LittleEndian.PutUint32(data[base+44:], info)
		binary.LittleEndian.PutUint64(data[base+48:], alignment)
		binary.LittleEndian.PutUint64(data[base+56:], entsize)
	}
	writeSection(1, dynstrName, elf.SHT_STRTAB, elf.SHF_ALLOC, dynstrOffset, len(dynamicStrings), 0, 0, 1, 0)
	writeSection(2, dynamicName, elf.SHT_DYNAMIC, elf.SHF_ALLOC|elf.SHF_WRITE, dynamicOffset, len(dynamicData), 1, 0, 8, 16)
	writeSection(3, shstrName, elf.SHT_STRTAB, 0, shstrOffset, len(shstr), 0, 0, 1, 0)

	path := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSectionlessTestELF(t *testing.T, needed, runpath string) string {
	t.Helper()

	dynstr := []byte{0}
	addString := func(value string) uint64 {
		offset := uint64(len(dynstr))
		dynstr = append(dynstr, value...)
		dynstr = append(dynstr, 0)
		return offset
	}
	neededOffset := addString(needed)
	runpathOffset := addString(runpath)

	const headerSize = 64
	const programHeaderSize = 56
	const programHeaderCount = 2
	dynstrOffset := headerSize + programHeaderSize*programHeaderCount
	dynamicOffset := (dynstrOffset + len(dynstr) + 15) &^ 15

	type tagValue struct {
		tag   elf.DynTag
		value uint64
	}
	tags := []tagValue{
		{elf.DT_NEEDED, neededOffset},
		{elf.DT_RUNPATH, runpathOffset},
		{elf.DT_STRTAB, uint64(dynstrOffset)},
		{elf.DT_STRSZ, uint64(len(dynstr))},
		{elf.DT_NULL, 0},
	}
	dynamicData := make([]byte, len(tags)*16)
	for i, tag := range tags {
		binary.LittleEndian.PutUint64(dynamicData[i*16:], uint64(tag.tag))
		binary.LittleEndian.PutUint64(dynamicData[i*16+8:], tag.value)
	}
	fileSize := dynamicOffset + len(dynamicData)
	data := make([]byte, fileSize)

	copy(data[0:4], []byte{0x7f, 'E', 'L', 'F'})
	data[4] = byte(elf.ELFCLASS64)
	data[5] = byte(elf.ELFDATA2LSB)
	data[6] = byte(elf.EV_CURRENT)
	binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_DYN))
	binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_X86_64))
	binary.LittleEndian.PutUint32(data[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint64(data[32:], headerSize)
	binary.LittleEndian.PutUint16(data[52:], headerSize)
	binary.LittleEndian.PutUint16(data[54:], programHeaderSize)
	binary.LittleEndian.PutUint16(data[56:], programHeaderCount)
	binary.LittleEndian.PutUint16(data[58:], 64)

	copy(data[dynstrOffset:], dynstr)
	copy(data[dynamicOffset:], dynamicData)

	writeProgramHeader := func(index int, typ elf.ProgType, offset, vaddr, size uint64) {
		base := headerSize + index*programHeaderSize
		binary.LittleEndian.PutUint32(data[base:], uint32(typ))
		binary.LittleEndian.PutUint32(data[base+4:], uint32(elf.PF_R))
		binary.LittleEndian.PutUint64(data[base+8:], offset)
		binary.LittleEndian.PutUint64(data[base+16:], vaddr)
		binary.LittleEndian.PutUint64(data[base+24:], vaddr)
		binary.LittleEndian.PutUint64(data[base+32:], size)
		binary.LittleEndian.PutUint64(data[base+40:], size)
		binary.LittleEndian.PutUint64(data[base+48:], 1)
	}
	writeProgramHeader(0, elf.PT_LOAD, 0, 0, uint64(fileSize))
	writeProgramHeader(1, elf.PT_DYNAMIC, uint64(dynamicOffset), uint64(dynamicOffset), uint64(len(dynamicData)))

	filePath := filepath.Join(t.TempDir(), "stripped-helper")
	if err := os.WriteFile(filePath, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return filePath
}
