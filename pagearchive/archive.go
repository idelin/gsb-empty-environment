package pagearchive

import (
	"encoding/binary"
	"fmt"
)

const (
	archiveMagic      = "PAGEARCH"
	archiveVersion    = byte(1)
	archiveHeaderSize = len(archiveMagic) + 1 + 2 + 4
	archiveRecordSize = 1 + 4 + 4
)

// Marshal serializes a layout into a compact self-describing archive.
func Marshal(layout *Layout) ([]byte, error) {
	if layout == nil {
		return nil, fmt.Errorf("%w: nil layout", ErrInvalidInput)
	}
	pageCount := len(layout.Representations)
	if pageCount == 0 || pageCount > MaxPages {
		return nil, fmt.Errorf("%w: layout page count must be in [1,%d]", ErrInvalidInput, MaxPages)
	}

	size := len(archiveMagic) + 1 + 2 + 4
	for _, representation := range layout.Representations {
		size += 1 + 4 + 4 + len(representation.Payload)
	}

	out := make([]byte, 0, size)
	out = append(out, archiveMagic...)
	out = append(out, archiveVersion)
	out = binary.LittleEndian.AppendUint16(out, uint16(pageCount))
	out = binary.LittleEndian.AppendUint32(out, 0)

	for page, representation := range layout.Representations {
		if representation.Page != page {
			return nil, fmt.Errorf("%w: representation %d has page id %d", ErrInvalidInput, page, representation.Page)
		}
		if len(representation.Payload) > 0xffffffff {
			return nil, fmt.Errorf("%w: page %d payload is too large", ErrInvalidInput, page)
		}
		flags := byte(0)
		if representation.Full {
			flags = 1
		}
		out = append(out, flags)
		out = binary.LittleEndian.AppendUint32(out, uint32(representation.Base))
		out = binary.LittleEndian.AppendUint32(out, uint32(len(representation.Payload)))
		out = append(out, representation.Payload...)
	}

	return out, nil
}

// Unmarshal parses an archive produced by Marshal. Call NewReader to validate
// references and reconstruct pages.
func Unmarshal(data []byte, pageSize, metadataCost int) (*Layout, error) {
	if pageSize <= 0 || metadataCost < 0 {
		return nil, fmt.Errorf("%w: invalid archive parameters", ErrInvalidInput)
	}
	if len(data) < archiveHeaderSize || string(data[:len(archiveMagic)]) != archiveMagic {
		return nil, fmt.Errorf("%w: bad archive magic", ErrInvalidInput)
	}
	if data[len(archiveMagic)] != archiveVersion {
		return nil, fmt.Errorf("%w: unsupported archive version", ErrInvalidInput)
	}
	pageCount := int(binary.LittleEndian.Uint16(data[9:11]))
	if pageCount == 0 || pageCount > MaxPages {
		return nil, fmt.Errorf("%w: invalid archive page count", ErrInvalidInput)
	}

	layout := &Layout{Representations: make([]Representation, pageCount)}
	pos := archiveHeaderSize
	totalCost := 0
	maxDepth := 0
	for page := 0; page < pageCount; page++ {
		if len(data[pos:]) < archiveRecordSize {
			return nil, fmt.Errorf("%w: truncated archive record header", ErrInvalidInput)
		}
		flags := data[pos]
		base := int(binary.LittleEndian.Uint32(data[pos+1 : pos+5]))
		payloadLen := int(binary.LittleEndian.Uint32(data[pos+5 : pos+9]))
		pos += archiveRecordSize

		if flags > 1 || len(data[pos:]) < payloadLen {
			return nil, fmt.Errorf("%w: truncated archive record", ErrInvalidInput)
		}
		payload := append([]byte(nil), data[pos:pos+payloadLen]...)
		pos += payloadLen

		representation := Representation{
			Page:         page,
			Full:         flags == 1,
			Base:         base,
			Payload:      payload,
			MetadataCost: metadataCost,
		}
		if representation.Full {
			if len(payload) != pageSize {
				return nil, fmt.Errorf("%w: full page %d has length %d, want %d", ErrInvalidInput, page, len(payload), pageSize)
			}
		}
		layout.Representations[page] = representation
		totalCost += representation.PersistentCost()
	}
	if pos != len(data) {
		return nil, fmt.Errorf("%w: archive has trailing bytes", ErrInvalidInput)
	}

	depths, err := validateSerializedLayout(layout)
	if err != nil {
		return nil, err
	}
	for _, depth := range depths {
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	layout.PersistentBytes = totalCost
	layout.DependencyDepth = maxDepth
	return layout, nil
}

func validateSerializedLayout(layout *Layout) ([]int, error) {
	pageCount := len(layout.Representations)
	depths := make([]int, pageCount)
	state := make([]uint8, pageCount)

	var visit func(int) (int, error)
	visit = func(page int) (int, error) {
		if state[page] == 1 {
			return 0, fmt.Errorf("%w: dependency graph contains a cycle at page %d", ErrInvalidInput, page)
		}
		if state[page] == 2 {
			return depths[page], nil
		}
		state[page] = 1
		representation := layout.Representations[page]
		depth := 0
		if !representation.Full {
			if representation.Base < 0 || representation.Base >= pageCount || representation.Base == page {
				return 0, fmt.Errorf("%w: page %d has invalid base", ErrInvalidInput, page)
			}
			baseDepth, err := visit(representation.Base)
			if err != nil {
				return 0, err
			}
			depth = baseDepth + 1
			if depth > MaxDependencyDepth {
				return 0, fmt.Errorf("%w: dependency depth exceeds %d", ErrInvalidInput, MaxDependencyDepth)
			}
		}
		state[page] = 2
		depths[page] = depth
		return depth, nil
	}

	for page := range layout.Representations {
		if _, err := visit(page); err != nil {
			return nil, err
		}
	}
	return depths, nil
}
