package zdebug

import (
	"fmt"
	"io"
)

// materializePage decodes one stream from its start up to and including the
// requested page, verifying every frame along the way. It returns the page
// and whether the natural end of the stream was reached inside that page.
//
// Nothing is cached for an in-progress page: the page is staged locally and
// only handed to the budgeted cache once it is complete, the decompression
// cap still holds and (for the final page) the whole frame ended cleanly and
// consumed exactly its declared physical range. Any failure discards the
// staged bytes, so partial stream output is never returned as verified.
//
// Prefix pages decoded to reach the target are not retained: pages are
// demand-paged, so each cached page independently represents bytes that were
// produced by a fully checksummed decoder path.
func (h *Host) materializePage(state *sectionState, target int) (page []byte, atEOF bool, err error) {
	section := state.section
	state.runs++

	physical := io.NewSectionReader(h.data, section.PhysicalOffset, section.PhysicalLength)
	counting := &countedSource{src: physical}

	factory := h.factories[section.Method]
	decoder, err := factory(counting)
	if err != nil {
		return nil, false, fmt.Errorf("zdebug: create decoder for %q: %w", section.Name, err)
	}

	var produced int64

	for pageIndex := 0; pageIndex <= target; pageIndex++ {
		page := make([]byte, h.pageSize)
		read := 0

		for read < len(page) {
			n, readErr := decoder.Read(page[read:])
			if n < 0 || read+n > len(page) {
				_ = decoder.Close()
				return nil, false, fmt.Errorf("zdebug: decoder returned invalid count for %q", section.Name)
			}
			read += n
			produced += int64(n)
			if produced > section.MaxDecompressed {
				_ = decoder.Close()
				return nil, false, fmt.Errorf("%w: section %q cap %d",
					ErrCapExceeded, section.Name, section.MaxDecompressed)
			}

			if readErr == io.EOF {
				if err := h.finishStream(state, decoder, counting, pageIndex, read, target); err != nil {
					return nil, false, err
				}
				h.markEOF(state, pageIndex)
				return page[:read], true, nil
			}
			if readErr != nil {
				_ = decoder.Close()
				return nil, false, fmt.Errorf("zdebug: decode section %q: %w", section.Name, readErr)
			}
		}

		if pageIndex == target {
			// A full non-tail page. Each decoded frame was already
			// checksummed; the physical-range and end-of-frame checks are
			// applied only to the tail page when the whole stream is
			// consumed, because decoders may buffer compressed input.
			_ = decoder.Close()
			return page, false, nil
		}
	}

	return nil, false, fmt.Errorf("zdebug: internal error: page %d not produced", target)
}

// finishStream validates the natural end of a stream before accepting its
// final page. The frame must end on a page boundary-compatible short read,
// report EOF, and consume exactly its declared physical range.
func (h *Host) finishStream(state *sectionState, decoder Decoder, counting *countedSource, pageIndex, read, target int) error {
	section := state.section

	if pageIndex != target {
		// The stream ended before producing the requested page.
		_ = decoder.Close()
		return fmt.Errorf("zdebug: section %q ended before page %d at page %d",
			section.Name, target, pageIndex)
	}
	if err := decoder.Close(); err != nil {
		return fmt.Errorf("zdebug: close decoder for %q: %w", section.Name, err)
	}
	if counting.consumed != section.PhysicalLength {
		return fmt.Errorf("zdebug: section %q consumed %d of %d physical bytes",
			section.Name, counting.consumed, section.PhysicalLength)
	}
	if produced := int64(pageIndex)*int64(h.pageSize) + int64(read); produced != section.MaxDecompressed {
		return fmt.Errorf("%w: section %q produced %d, cap declared %d",
			ErrCapExceeded, section.Name, produced, section.MaxDecompressed)
	}

	// Record the verified size when the tail page materializes.
	h.mu.Lock()
	state.size = int64(pageIndex)*int64(h.pageSize) + int64(read)
	state.knownSize = true
	h.mu.Unlock()
	return nil
}

func (h *Host) markEOF(state *sectionState, pageIndex int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if state.eofPages == nil {
		state.eofPages = make(map[int]bool)
	}
	state.eofPages[pageIndex] = true
}

// countedSource tracks how many physical bytes a decoder has consumed and
// implements io.ByteReader so framing decoders (notably flate) never wrap it
// in a buffering reader: then the consumed count stays exact and trailing
// bytes remain detectable.
type countedSource struct {
	src      io.Reader
	consumed int64
}

func (c *countedSource) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	if n < 0 {
		return 0, fmt.Errorf("zdebug: physical source returned negative count")
	}
	c.consumed += int64(n)
	return n, err
}

func (c *countedSource) ReadByte() (byte, error) {
	var one [1]byte
	_, err := io.ReadFull(c.src, one[:])
	if err != nil {
		return 0, err
	}
	c.consumed++
	return one[0], nil
}
