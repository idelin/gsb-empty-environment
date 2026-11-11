package fmp4mux

import "bytes"

// Fragmenter muxes H.264 access units into fMP4 initialization and media
// segments.
//
// Media segments are produced lazily: PushSample buffers samples until the
// next IDR, at which point the previous fragment is finalized. The duration
// of its last sample is derived from the new IDR's DTS. Close flushes the
// final fragment.
//
// The sidx box is returned separately from the media segment (which holds
// styp/moof/mdat); concatenate sidx before the segment bytes when the target
// container expects sidx inline. The sidx first_offset is 0, i.e. the moof
// immediately follows the sidx.
type Fragmenter struct {
	cfg       Config
	avcC      []byte
	initSeg   []byte
	pending   []fragSample
	seqNumber uint32
	closed    bool
}

type fragSample struct {
	dts uint64
	cto int32
	au  []byte // AVCC (length-prefixed) sample
	key bool
}

// NewFragmenter creates a muxer. The initialization segment is generated
// lazily from the first IDR (so SPS/PPS can be auto-extracted), or eagerly
// when cfg carries explicit SPS/PPS.
func NewFragmenter(cfg Config) *Fragmenter {
	if cfg.Timescale == 0 {
		cfg.Timescale = 90000
	}
	if cfg.SequenceNumber == 0 {
		cfg.SequenceNumber = 1
	}
	f := &Fragmenter{cfg: cfg, seqNumber: cfg.SequenceNumber}
	if cfg.SPS != nil && cfg.PPS != nil {
		f.avcC = buildAVCC(cfg.SPS, cfg.PPS)
		f.initSeg = buildInit(cfg, f.avcC)
	}
	return f
}

// InitSegment returns the initialization segment. It is available once the
// first IDR sample has been pushed (parameter sets may have been extracted
// from it) or when SPS/PPS were supplied in Config.
func (f *Fragmenter) InitSegment() []byte {
	return f.initSeg
}

// PushSample appends an access unit in decode order. The first sample of a
// fragment (and the very first sample overall) must be an IDR; otherwise
// ErrFragmentStart is returned and no state is modified.
//
// When a new IDR arrives while samples are buffered, the previous fragment is
// finalized and returned together with its sidx box, and the IDR starts the
// next pending fragment.
func (f *Fragmenter) PushSample(s Sample) (mediaSegment []byte, sidxBox []byte, err error) {
	if f.closed {
		return nil, nil, ErrClosed
	}

	avcc := toAVCSample(s.AU)
	key := s.Key || containsIDR(s.AU)

	// A new fragment must start on an IDR.
	if len(f.pending) == 0 && !key {
		return nil, nil, ErrFragmentStart
	}

	if f.initSeg == nil {
		if !key {
			return nil, nil, ErrFragmentStart
		}
		sps, pps := f.cfg.SPS, f.cfg.PPS
		if sps == nil || pps == nil {
			autoSPS, autoPPS := extractParameterSets(s.AU)
			if sps == nil {
				sps = autoSPS
			}
			if pps == nil {
				pps = autoPPS
			}
		}
		if sps == nil || pps == nil {
			return nil, nil, ErrFragmentStart
		}
		f.avcC = buildAVCC(sps, pps)
		f.initSeg = buildInit(f.cfg, f.avcC)
	}

	if key && len(f.pending) > 0 {
		mediaSegment, sidxBox, err = f.flush(f.pending, s.DTS)
		if err != nil {
			return nil, nil, err
		}
		f.pending = nil
	}

	f.pending = append(f.pending, fragSample{
		dts: s.DTS,
		cto: s.CTO,
		au:  avcc,
		key: key,
	})
	return mediaSegment, sidxBox, nil
}

// Close flushes the final fragment. With no successor DTS available, the last
// sample duration defaults to the spacing of the preceding two samples.
func (f *Fragmenter) Close() (mediaSegment []byte, sidxBox []byte, err error) {
	if f.closed {
		return nil, nil, nil
	}
	f.closed = true
	if len(f.pending) == 0 {
		return nil, nil, nil
	}
	mediaSegment, sidxBox, err = f.flush(f.pending, 0)
	if err != nil {
		return nil, nil, err
	}
	f.pending = nil
	return mediaSegment, sidxBox, nil
}

// flush finalizes one fragment. nextDTS is the DTS of the first sample of the
// following fragment, used to derive the duration of the last sample; zero
// means this is the final fragment.
func (f *Fragmenter) flush(samples []fragSample, nextDTS uint64) ([]byte, []byte, error) {
	n := len(samples)
	durations := make([]uint32, n)
	for i := 0; i+1 < n; i++ {
		durations[i] = uint32(samples[i+1].dts - samples[i].dts)
	}
	if nextDTS != 0 {
		durations[n-1] = uint32(nextDTS - samples[n-1].dts)
	} else {
		durations[n-1] = lastSampleDuration(samples)
	}

	earliestPTS := int64(samples[0].dts) + int64(samples[0].cto)
	for _, smp := range samples[1:] {
		if pts := int64(smp.dts) + int64(smp.cto); pts < earliestPTS {
			earliestPTS = pts
		}
	}
	if earliestPTS < 0 {
		return nil, nil, ErrNegativePTS
	}

	moof := buildMoof(f.seqNumber, samples, durations)
	mdat := buildMdat(samples)

	// trun.data_offset: byte distance from moof start to mdat payload start.
	moofSize := uint32(len(moof))
	mdatHeader := uint32(8)
	dataOffset := int32(moofSize + mdatHeader)
	patchTrunDataOffset(moof, dataOffset)

	styp := makeBox("styp", stypPayload())
	segment := make([]byte, 0, len(styp)+len(moof)+len(mdat))
	segment = append(segment, styp...)
	segment = append(segment, moof...)
	segment = append(segment, mdat...)

	stypSize := uint32(8 + len(stypPayload()))
	mdatSize := uint32(len(mdat))
	sidx := buildSIDX(f.cfg.Timescale, samples, durations, stypSize+moofSize+mdatSize)
	f.seqNumber++
	return segment, sidx, nil
}

func lastSampleDuration(samples []fragSample) uint32 {
	n := len(samples)
	if n >= 2 {
		return uint32(samples[n-1].dts - samples[n-2].dts)
	}
	return 0
}

func stypPayload() []byte {
	var w bw
	w.fourcc("msdh")
	w.u32(0)
	w.fourcc("msdh")
	w.fourcc("msix")
	w.fourcc("iso6")
	return w.Bytes()
}

// Sample flags used in trun (ISO/IEC 14496-12, sample flags layout):
// sync sample (I/IDR) has sample_depends_on=2; non-sync adds
// sample_is_non_sync_sample=1 and uses sample_depends_on=1.
const (
	flagSyncSample    uint32 = 0x02000000
	flagNonSyncSample uint32 = 0x01010000
)

func buildMoof(seq uint32, samples []fragSample, durations []uint32) []byte {
	baseDTS := samples[0].dts

	mfhd := makeFullBox("mfhd", 0, 0, func() []byte {
		var w bw
		w.u32(seq)
		return w.Bytes()
	}())

	tfhd := makeFullBox("tfhd", 0, 0x020000, func() []byte {
		var w bw
		w.u32(1) // track_ID; flag default-base-is-moof set
		return w.Bytes()
	}())

	tfdt := makeFullBox("tfdt", 1, 0, func() []byte {
		var w bw
		w.u64(baseDTS)
		return w.Bytes()
	}())

	// Decide whether any composition offset is non-zero; then use signed
	// version 1 trun (needed for negative offsets caused by B frames).
	hasCTO := false
	for _, s := range samples {
		if s.cto != 0 {
			hasCTO = true
			break
		}
	}

	var trunInner bytes.Buffer
	var h bw
	h.u32(uint32(len(samples)))
	h.u32(0) // data_offset, patched later via patchTrunDataOffset
	trunInner.Write(h.Bytes())

	var tw bw
	for i, s := range samples {
		tw.u32(durations[i])
		tw.u32(uint32(len(s.au)))
		if s.key {
			tw.u32(flagSyncSample)
		} else {
			tw.u32(flagNonSyncSample)
		}
		if hasCTO {
			tw.u32(uint32(s.cto))
		}
	}
	trunInner.Write(tw.Bytes())

	// flags: data-offset (0x1) | sample-duration (0x100) | sample-size
	// (0x200) | sample-flags (0x400) | sample-composition-time-offsets
	// (0x800). Every sample carries explicit flags, so first-sample-flags
	// (0x4) is not used.
	flags := uint32(0x000001 | 0x000100 | 0x000200 | 0x000400)
	version := uint8(0)
	if hasCTO {
		flags |= 0x000800
		version = 1
	}
	trun := makeFullBox("trun", version, flags, trunInner.Bytes())

	traf := container("traf", tfhd, tfdt, trun)
	return container("moof", mfhd, traf)
}

func buildMdat(samples []fragSample) []byte {
	total := 0
	for _, s := range samples {
		total += len(s.au)
	}
	var w bw
	w.u32(uint32(8 + total))
	w.fourcc("mdat")
	for _, s := range samples {
		w.b(s.au)
	}
	return w.Bytes()
}

// patchTrunDataOffset overwrites trun.data_offset. With idx pointing at the
// "trun" type field: version/flags start at idx+4, sample_count at idx+8 and
// data_offset at idx+12.
func patchTrunDataOffset(moof []byte, dataOffset int32) {
	idx := bytes.Index(moof, []byte("trun"))
	if idx < 0 {
		return
	}
	pos := idx + 12
	binaryBigEndianPutU32(moof[pos:pos+4], uint32(dataOffset))
}

func binaryBigEndianPutU32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

func buildSIDX(timescale uint32, samples []fragSample, durations []uint32, referencedSize uint32) []byte {
	var subsegmentDuration uint64
	for _, d := range durations {
		subsegmentDuration += uint64(d)
	}

	// The first sample is an IDR: SAP type 1 (closed GOP), and the SAP is the
	// referenced item itself so sap_delta_time is its offset from EPT.
	earliestPTS := int64(samples[0].dts) + int64(samples[0].cto)
	for _, s := range samples[1:] {
		if pts := int64(s.dts) + int64(s.cto); pts < earliestPTS {
			earliestPTS = pts
		}
	}
	sapPTS := int64(samples[0].dts) + int64(samples[0].cto)
	sapDelta := sapPTS - earliestPTS

	var p bw
	p.u32(1)         // reference_ID
	p.u32(timescale) // timescale
	p.u64(uint64(earliestPTS))
	p.u64(0) // first_offset: sidx directly precedes moof => 0
	p.u16(0) // reserved
	p.u16(1) // reference_count
	p.u32(referencedSize)
	p.u64(subsegmentDuration)
	p.u32((1 << 28) | (1 << 24) | uint32(sapDelta))
	return makeFullBox("sidx", 1, 0, p.Bytes())
}
