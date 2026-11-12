package netsec

import (
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
)

// 三个内嵌节的节名。
const (
	SectionUpdateInfo = ".upd_info"
	SectionSig        = ".sha256_sig"
	SectionKey        = ".sig_key"
)

var (
	ErrNotELF         = errors.New("netsec: not an ELF64 file")
	ErrMissingSection = errors.New("netsec: required section missing")
	ErrSectionBounds  = errors.New("netsec: section offset+size out of file bounds")
	ErrSectionOverlap = errors.New("netsec: sections overlap")
)

// Section 描述一个已校验边界的节在文件中的位置。
type Section struct {
	Name   string
	Offset uint64
	Size   uint64
}

// Sections 保存更新判定所需的三个节。
type Sections struct {
	UpdateInfo Section
	Sig        Section
	Key        Section
}

// Bytes 返回节在 data 中的内容。
func (s Section) Bytes(data []byte) []byte {
	return data[s.Offset : s.Offset+s.Size]
}

// FindSections 解析 ELF64 节头，定位 .upd_info、.sha256_sig、.sig_key。
// 任一节偏移加大小越界，或三个节互相重叠，即拒绝。
func FindSections(data []byte) (*Sections, error) {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotELF, err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 {
		return nil, fmt.Errorf("%w: class %s", ErrNotELF, f.Class)
	}

	out := &Sections{}
	found := map[string]*Section{
		SectionUpdateInfo: &out.UpdateInfo,
		SectionSig:        &out.Sig,
		SectionKey:        &out.Key,
	}
	for _, sec := range f.Sections {
		dst, ok := found[sec.Name]
		if !ok {
			continue
		}
		dst.Name = sec.Name
		if sec.Type == elf.SHT_NOBITS {
			// 不占文件空间，文件内范围为空。
			dst.Offset, dst.Size = 0, 0
			continue
		}
		off, size := sec.Offset, sec.Size
		// 溢出安全的越界检查：off+size 不得超出文件长度。
		if off > uint64(len(data)) || size > uint64(len(data))-off {
			return nil, fmt.Errorf("%w: %s off=%d size=%d filelen=%d",
				ErrSectionBounds, sec.Name, off, size, len(data))
		}
		dst.Offset, dst.Size = off, size
	}
	for name, sec := range found {
		if sec.Name == "" {
			return nil, fmt.Errorf("%w: %s", ErrMissingSection, name)
		}
	}

	// 三节两两不得重叠（零长度节不与任何节重叠）。
	list := []Section{out.UpdateInfo, out.Sig, out.Key}
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if rangesOverlap(list[i], list[j]) {
				return nil, fmt.Errorf("%w: %s and %s",
					ErrSectionOverlap, list[i].Name, list[j].Name)
			}
		}
	}
	return out, nil
}

func rangesOverlap(a, b Section) bool {
	if a.Size == 0 || b.Size == 0 {
		return false
	}
	return a.Offset < b.Offset+b.Size && b.Offset < a.Offset+a.Size
}
