package main

import (
	"errors"
	"fmt"
	"sort"
)

type Event struct {
	Seq        int         `json:"seq"`
	Type       string      `json:"type"`
	Name       string      `json:"name,omitempty"`
	Value      string      `json:"value,omitempty"`
	Size       int         `json:"size,omitempty"`
	AbsIndex   int         `json:"abs_index,omitempty"`
	Capacity   int         `json:"capacity,omitempty"`
	StreamID   int         `json:"stream_id,omitempty"`
	EncodedRIC int         `json:"encoded_ric,omitempty"`
	Sign       int         `json:"sign,omitempty"`
	DeltaBase  int         `json:"delta_base,omitempty"`
	References []Reference `json:"references,omitempty"`
}

type Reference struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

type tableEntry struct {
	AbsIndex int    `json:"abs_index"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Size     int    `json:"size"`
}

type headerBlock struct {
	ric        int
	base       int
	absRefs    []int
	evicted    bool
	evictedRef int
	invalid    error
}

type BlockResult struct {
	StreamID            int    `json:"stream_id"`
	ArrivalSeq          int    `json:"arrival_seq"`
	RequiredInsertCount int    `json:"required_insert_count"`
	Base                int    `json:"base"`
	AbsoluteReferences  []int  `json:"absolute_references"`
	FirstDecodableSeq   *int   `json:"first_decodable_seq,omitempty"`
	Status              string `json:"status"`
	Error               string `json:"error,omitempty"`
}

type TableState struct {
	OldestAbsIndex int          `json:"oldest_abs_index"`
	InsertCount    int          `json:"insert_count"`
	Capacity       int          `json:"capacity"`
	UsedSize       int          `json:"used_size"`
	Entries        []tableEntry `json:"entries"`
}

type ReplayResult struct {
	Blocks     []BlockResult `json:"blocks"`
	FinalTable TableState    `json:"final_table"`
}

type Replayer struct {
	entries    []tableEntry
	inserted   int
	oldest     int
	capacity   int
	maxEntries int
	used       int
	blocks     map[int]*headerBlock
	order      []int
	results    map[int]*BlockResult
}

func NewReplayer(initialCapacity, maxEntries int) *Replayer {
	if initialCapacity < 0 {
		initialCapacity = 0
	}
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &Replayer{
		capacity:   initialCapacity,
		maxEntries: maxEntries,
		blocks:     make(map[int]*headerBlock),
		results:    make(map[int]*BlockResult),
	}
}

func (r *Replayer) Run(events []Event) (ReplayResult, error) {
	autoSeq := 1
	for _, event := range events {
		seq := event.Seq
		if seq == 0 {
			seq = autoSeq
		}
		autoSeq = seq + 1
		event.Seq = seq

		var err error
		switch event.Type {
		case "insert":
			err = r.insert(event)
		case "duplicate":
			err = r.duplicate(event)
		case "capacity":
			err = r.setCapacity(event)
		case "header_block":
			err = r.headerBlock(event)
		default:
			err = fmt.Errorf("seq %d: unknown event type %q", seq, event.Type)
		}
		if err != nil {
			return ReplayResult{}, err
		}

		r.refreshBlocks(seq)
	}

	return r.result(), nil
}

func (r *Replayer) insert(event Event) error {
	if event.Size < 0 {
		return fmt.Errorf("seq %d: negative entry size", event.Seq)
	}
	size := event.Size
	if size == 0 {
		size = entrySize(event.Name, event.Value)
	}
	if size > r.capacity {
		return fmt.Errorf("seq %d: entry size %d exceeds capacity %d", event.Seq, size, r.capacity)
	}

	for r.used+size > r.capacity && len(r.entries) > 0 {
		r.evictOldest()
	}

	entry := tableEntry{
		AbsIndex: r.inserted,
		Name:     event.Name,
		Value:    event.Value,
		Size:     size,
	}
	r.entries = append(r.entries, entry)
	r.used += size
	r.inserted++
	return nil
}

func (r *Replayer) duplicate(event Event) error {
	if event.AbsIndex < r.oldest || event.AbsIndex >= r.inserted {
		return fmt.Errorf("seq %d: duplicate absolute index %d is not in the dynamic table", event.Seq, event.AbsIndex)
	}

	source := r.entries[event.AbsIndex-r.oldest]
	for r.used+source.Size > r.capacity && len(r.entries) > 0 {
		r.evictOldest()
		if r.oldest > source.AbsIndex {
			return fmt.Errorf("seq %d: duplicated entry %d was evicted before copy completed", event.Seq, source.AbsIndex)
		}
		source = r.entries[source.AbsIndex-r.oldest]
	}

	entry := tableEntry{
		AbsIndex: r.inserted,
		Name:     source.Name,
		Value:    source.Value,
		Size:     source.Size,
	}
	r.entries = append(r.entries, entry)
	r.used += source.Size
	r.inserted++
	return nil
}

func (r *Replayer) setCapacity(event Event) error {
	if event.Capacity < 0 {
		return fmt.Errorf("seq %d: negative capacity", event.Seq)
	}
	r.capacity = event.Capacity
	for r.used > r.capacity && len(r.entries) > 0 {
		r.evictOldest()
	}
	return nil
}

func (r *Replayer) headerBlock(event Event) error {
	if _, exists := r.blocks[event.StreamID]; exists {
		return fmt.Errorf("seq %d: stream %d already has a blocked header block", event.Seq, event.StreamID)
	}
	if _, exists := r.results[event.StreamID]; exists {
		return fmt.Errorf("seq %d: stream %d already emitted a header block", event.Seq, event.StreamID)
	}
	if event.Sign != 0 && event.Sign != 1 {
		return fmt.Errorf("seq %d: sign must be 0 or 1", event.Seq)
	}

	ric, err := decodeRequiredInsertCount(event.EncodedRIC, r.inserted, r.maxEntries)
	if err != nil {
		return fmt.Errorf("seq %d: %w", event.Seq, err)
	}

	base := event.DeltaBase
	if event.Sign == 0 {
		base = ric - event.DeltaBase
	}
	if base < 0 {
		return fmt.Errorf("seq %d: decoded base %d is negative", event.Seq, base)
	}

	block := &headerBlock{
		ric:  ric,
		base: base,
	}

	for _, ref := range event.References {
		absRef, err := absoluteReference(ref, base)
		if err != nil {
			block.invalid = fmt.Errorf("seq %d: %w", event.Seq, err)
			break
		}
		if absRef < 0 {
			block.invalid = fmt.Errorf("seq %d: reference %s[%d] decodes to negative absolute index %d", event.Seq, ref.Kind, ref.Index, absRef)
			break
		}
		block.absRefs = append(block.absRefs, absRef)
		if isPostBaseReference(ref.Kind) && absRef >= ric {
			block.invalid = fmt.Errorf("seq %d: post-base absolute index %d is not smaller than required insert count %d", event.Seq, absRef, ric)
			break
		}
		if !isPostBaseReference(ref.Kind) && absRef >= ric {
			block.invalid = fmt.Errorf("seq %d: relative absolute index %d is not smaller than required insert count %d", event.Seq, absRef, ric)
			break
		}
		if absRef < r.oldest {
			block.evicted = true
			block.evictedRef = absRef
			break
		}
	}

	result := &BlockResult{
		StreamID:            event.StreamID,
		ArrivalSeq:          event.Seq,
		RequiredInsertCount: ric,
		Base:                base,
		AbsoluteReferences:  append([]int(nil), block.absRefs...),
		Status:              "blocked",
	}

	if block.invalid != nil {
		result.Status = "invalid"
		result.Error = block.invalid.Error()
		r.results[event.StreamID] = result
		return nil
	}
	if block.evicted {
		result.Status = "evicted"
		result.Error = fmt.Sprintf("absolute index %d is older than current oldest index %d", block.evictedRef, r.oldest)
		r.results[event.StreamID] = result
		return nil
	}
	if ric > r.inserted || hasFutureReference(block.absRefs, r.inserted) {
		r.blocks[event.StreamID] = block
		r.order = append(r.order, event.StreamID)
		r.results[event.StreamID] = result
	} else {
		result.Status = "decodable"
		result.FirstDecodableSeq = &event.Seq
		r.results[event.StreamID] = result
	}
	return nil
}

func (r *Replayer) refreshBlocks(seq int) {
	for _, streamID := range r.order {
		block, exists := r.blocks[streamID]
		if !exists {
			continue
		}
		for _, absRef := range block.absRefs {
			if absRef < r.oldest {
				block.evicted = true
				block.evictedRef = absRef
				break
			}
		}
		if block.evicted {
			result := r.results[streamID]
			result.Status = "evicted"
			result.Error = fmt.Sprintf("absolute index %d was evicted while stream was blocked", block.evictedRef)
			delete(r.blocks, streamID)
			continue
		}
		if r.inserted >= block.ric && !hasFutureReference(block.absRefs, r.inserted) {
			result := r.results[streamID]
			result.Status = "decodable"
			result.FirstDecodableSeq = &seq
			delete(r.blocks, streamID)
		}
	}

	kept := r.order[:0]
	for _, streamID := range r.order {
		if _, exists := r.blocks[streamID]; exists {
			kept = append(kept, streamID)
		}
	}
	r.order = kept
}

func (r *Replayer) evictOldest() {
	if len(r.entries) == 0 {
		return
	}
	entry := r.entries[0]
	r.entries = r.entries[1:]
	r.used -= entry.Size
	r.oldest++
}

func (r *Replayer) result() ReplayResult {
	for _, streamID := range r.order {
		if block, exists := r.blocks[streamID]; exists {
			result := r.results[streamID]
			result.Status = "undecodable"
			result.Error = fmt.Sprintf("insert count %d is below required insert count %d", r.inserted, block.ric)
		}
	}

	streamIDs := make([]int, 0, len(r.results))
	for streamID := range r.results {
		streamIDs = append(streamIDs, streamID)
	}
	sort.Ints(streamIDs)

	blocks := make([]BlockResult, 0, len(streamIDs))
	for _, streamID := range streamIDs {
		blocks = append(blocks, *r.results[streamID])
	}

	return ReplayResult{
		Blocks: blocks,
		FinalTable: TableState{
			OldestAbsIndex: r.oldest,
			InsertCount:    r.inserted,
			Capacity:       r.capacity,
			UsedSize:       r.used,
			Entries:        append([]tableEntry(nil), r.entries...),
		},
	}
}

func decodeRequiredInsertCount(encodedRIC, fullRange, maxEntries int) (int, error) {
	const maxEncodedRIC = 255
	if encodedRIC < 0 || maxEntries < 0 || encodedRIC > maxEncodedRIC || encodedRIC > 2*maxEntries {
		return 0, errors.New("invalid required insert count arguments")
	}
	if encodedRIC == 0 {
		if fullRange == 0 {
			return 0, nil
		}
		return 0, errors.New("encoded required insert count 0 is invalid when dynamic entries exist")
	}
	if fullRange == 0 {
		return 0, errors.New("non-zero encoded required insert count before any dynamic insertion")
	}
	if maxEntries == 0 {
		return 0, errors.New("non-zero required insert count with zero maximum entries")
	}

	wrap := 2 * maxEntries
	ric := encodedRIC + (fullRange/wrap)*wrap
	if ric <= fullRange-maxEntries {
		ric += wrap
	}
	return ric, nil
}

func absoluteReference(ref Reference, base int) (int, error) {
	switch ref.Kind {
	case "indexed", "name":
		return base - ref.Index, nil
	case "post_base", "post_base_name":
		return base + 1 + ref.Index, nil
	default:
		return 0, fmt.Errorf("unknown reference kind %q", ref.Kind)
	}
}

func isPostBaseReference(kind string) bool {
	return kind == "post_base" || kind == "post_base_name"
}

func hasFutureReference(refs []int, insertCount int) bool {
	for _, ref := range refs {
		if ref >= insertCount {
			return true
		}
	}
	return false
}

func entrySize(name, value string) int {
	return len(name) + len(value) + 32
}
