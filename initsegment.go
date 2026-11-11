package fmp4mux

// buildInit builds the initialization segment: ftyp + moov (with mvex/trex).
// The movie and the track both use cfg.Timescale.
func buildInit(cfg Config, avcC []byte) []byte {
	ftyp := makeBox("ftyp", ftypPayload())
	moov := container("moov",
		mvhdBox(cfg.Timescale),
		trakBox(cfg, avcC),
		mvexBox(),
	)
	return append(ftyp, moov...)
}

func ftypPayload() []byte {
	var w bw
	w.fourcc("iso5") // major brand
	w.u32(1)         // minor version
	w.fourcc("iso5")
	w.fourcc("iso6")
	w.fourcc("mp41")
	w.fourcc("avc1")
	return w.Bytes()
}

func mvhdBox(timescale uint32) []byte {
	var w bw
	w.u32(0) // creation_time
	w.u32(0) // modification_time
	w.u32(timescale)
	w.u32(0)          // duration unknown for live
	w.u32(0x00010000) // rate 1.0
	w.u16(0x0100)     // volume
	w.u16(0)          // reserved
	w.u64(0)          // reserved
	appendMatrix(&w)
	// pre_defined (6 * uint32)
	for i := 0; i < 6; i++ {
		w.u32(0)
	}
	w.u32(2) // next_track_ID
	return makeFullBox("mvhd", 0, 0, w.Bytes())
}

func appendMatrix(w *bw) {
	w.u32(0x00010000) // a
	w.u32(0)          // b
	w.u32(0)          // u
	w.u32(0)          // c
	w.u32(0x00010000) // d
	w.u32(0)          // v
	w.u32(0)          // x
	w.u32(0)          // y
	w.u32(0x40000000) // w (16.16 fixed point 1.0)
}

func trakBox(cfg Config, avcC []byte) []byte {
	return container("trak",
		tkhdBox(cfg),
		mdiaBox(cfg, avcC),
	)
}

func tkhdBox(cfg Config) []byte {
	var w bw
	w.u32(0) // creation_time
	w.u32(0) // modification_time
	w.u32(1) // track_ID
	w.u32(0) // reserved
	w.u32(0) // duration
	w.u64(0) // reserved
	w.u16(0) // layer
	w.u16(0) // alternate_group
	w.u16(0) // volume
	w.u16(0) // reserved
	appendMatrix(&w)
	w.u32(uint32(cfg.Width) << 16)  // width 16.16
	w.u32(uint32(cfg.Height) << 16) // height 16.16
	// flags: track_enabled | track_in_movie | track_in_preview
	return makeFullBox("tkhd", 0, 0x000007, w.Bytes())
}

func mdiaBox(cfg Config, avcC []byte) []byte {
	return container("mdia",
		mdhdBox(cfg.Timescale),
		hdlrBox(),
		minfBox(cfg, avcC),
	)
}

func mdhdBox(timescale uint32) []byte {
	var w bw
	w.u32(0) // creation_time
	w.u32(0) // modification_time
	w.u32(timescale)
	w.u32(0)      // duration
	w.u16(0x55c4) // language 'und'
	w.u16(0)      // pre_defined
	return makeFullBox("mdhd", 0, 0, w.Bytes())
}

func hdlrBox() []byte {
	var w bw
	w.u32(0) // pre_defined
	w.fourcc("vide")
	w.u32(0) // reserved[3]
	w.u32(0)
	w.u32(0)
	w.b([]byte("VideoHandler\x00"))
	return makeFullBox("hdlr", 0, 0, w.Bytes())
}

func minfBox(cfg Config, avcC []byte) []byte {
	return container("minf",
		vmhdBox(),
		dinfBox(),
		stblBox(cfg, avcC),
	)
}

func vmhdBox() []byte {
	var w bw
	w.u16(0) // graphicsmode
	for i := 0; i < 3; i++ {
		w.u16(0) // opcolor
	}
	return makeFullBox("vmhd", 0, 0x000001, w.Bytes())
}

func dinfBox() []byte {
	url := makeFullBox("url ", 0, 0x000001, nil) // self-contained
	dref := makeFullBox("dref", 0, 0, func() []byte {
		var w bw
		w.u32(1) // entry_count
		w.b(url)
		return w.Bytes()
	}())
	return container("dinf", dref)
}

func stblBox(cfg Config, avcC []byte) []byte {
	return container("stbl",
		stsdBox(cfg, avcC),
		timeToSampleBox(),
		sampleToChunkBox(),
		sampleSizeBox(),
		chunkOffsetBox(),
	)
}

func stsdBox(cfg Config, avcC []byte) []byte {
	var w bw
	w.u32(1) // entry_count
	w.b(avc1SampleEntry(cfg, avcC))
	return makeFullBox("stsd", 0, 0, w.Bytes())
}

func avc1SampleEntry(cfg Config, avcC []byte) []byte {
	var w bw
	w.u16(0) // reserved[6]
	w.u16(0)
	w.u16(0)
	w.u16(1) // data_reference_index
	w.u16(0) // pre_defined
	w.u16(0) // reserved
	w.u32(0) // pre_defined[3]
	w.u32(0)
	w.u32(0)
	w.u16(cfg.Width)
	w.u16(cfg.Height)
	w.u32(0x00480000) // horizresolution 72 dpi
	w.u32(0x00480000) // vertresolution 72 dpi
	w.u32(0)          // reserved
	w.u16(1)          // frame_count
	var compressor [32]byte
	compressor[0] = byte(len("AVC Coding"))
	copy(compressor[1:], "AVC Coding")
	w.b(compressor[:])
	w.u16(0x0018) // depth
	w.u16(0xffff) // pre_defined
	w.b(makeBox("avcC", avcC))
	return makeBox("avc1", w.Bytes())
}

func timeToSampleBox() []byte {
	var w bw
	w.u32(0) // entry_count (all samples live in fragments)
	return makeFullBox("stts", 0, 0, w.Bytes())
}

func sampleToChunkBox() []byte {
	var w bw
	w.u32(0)
	return makeFullBox("stsc", 0, 0, w.Bytes())
}

func sampleSizeBox() []byte {
	var w bw
	w.u32(0) // sample_size (per-sample sizes live in trun)
	w.u32(0) // sample_count
	return makeFullBox("stsz", 0, 0, w.Bytes())
}

func chunkOffsetBox() []byte {
	var w bw
	w.u32(0) // entry_count
	return makeFullBox("stco", 0, 0, w.Bytes())
}

func mvexBox() []byte {
	return container("mvex", trexBox())
}

func trexBox() []byte {
	var w bw
	w.u32(1) // track_ID
	w.u32(1) // default_sample_description_index
	w.u32(0) // default_sample_duration
	w.u32(0) // default_sample_size
	w.u32(0) // default_sample_flags
	return makeFullBox("trex", 0, 0, w.Bytes())
}
