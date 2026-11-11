package tsdemux

import (
	"bytes"
	"testing"
)

func newDemuxedStream(t *testing.T) *tsBuilder {
	t.Helper()
	b := newTSBuilder()
	b.emitSection(PIDPAT, buildPAT(0))
	b.emitSection(testPMTPID, buildPMT(0))
	rd := NewReader(bytes.NewReader(b.bytes()))
	if events := drain(t, rd); len(events) != 0 {
		t.Fatalf("PSI-only stream produced %d events", len(events))
	}
	programs := rd.Programs()
	if len(programs) != 1 || programs[0].PMTPID != testPMTPID ||
		programs[0].PCRPID != testVideoPID || len(programs[0].Streams) != 2 {
		t.Fatalf("unexpected programs after PAT/PMT: %+v", programs)
	}
	return b
}

// 正常流：跨多包 PES 的边界、负载与 PTS/DTS 必须完整还原。
func TestPESReassemblyAndBoundaries(t *testing.T) {
	b := newDemuxedStream(t)

	payload1 := bytes.Repeat([]byte{0xAA}, 300) // 跨越 2 个 TS 包
	payload2 := bytes.Repeat([]byte{0xBB}, 500) // 跨越 3 个 TS 包
	pes1 := buildPES(0xE0, 90000, 81000, true, payload1)
	pes2 := buildPES(0xE0, 180000, 0, false, payload2)
	b.emitPES(testVideoPID, pes1)
	b.emitPES(testVideoPID, pes2)

	rd := NewReader(bytes.NewReader(b.bytes()))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 2 {
		t.Fatalf("expected 2 PES, got %d", len(pes))
	}

	if !bytes.Equal(pes[0].Data, payload1) || !bytes.Equal(pes[1].Data, payload2) {
		t.Fatal("reassembled PES payloads do not match inputs")
	}
	if pes[0].StreamID != 0xE0 || pes[0].Corrupted {
		t.Fatalf("unexpected first PES metadata: %+v", pes[0])
	}
	if !pes[0].HasPTS || pes[0].PTS != 90000 || !pes[0].HasDTS || pes[0].DTS != 81000 {
		t.Fatalf("unexpected timestamps: PTS=%d DTS=%d", pes[0].PTS, pes[0].DTS)
	}
	if pes[1].HasDTS || pes[1].PTS != 180000 {
		t.Fatalf("second PES timestamps wrong: %+v", pes[1])
	}
	if pes[0].PTSUnwrapped != 90000 || pes[0].DTSUnwrapped != 81000 {
		t.Fatalf("unwrapped timestamps wrong: %d %d", pes[0].PTSUnwrapped, pes[0].DTSUnwrapped)
	}
}

// 注入丢包：CC 跳变后受损 PES 被标记，下一 PUSI 重新对齐且新 PES 完好。
func TestContinuityGapDropsDamagedPES(t *testing.T) {
	b := newDemuxedStream(t)

	payload1 := bytes.Repeat([]byte{0x11}, 400)
	payload2 := bytes.Repeat([]byte{0x22}, 100)
	pes1 := buildPES(0xE0, 1000, 0, false, payload1)
	pes2 := buildPES(0xE0, 2000, 0, false, payload2)

	// PES1：起始包 + 一个中间包，然后丢弃一个中间包。
	const maxPayload = PacketSize - 4
	firstChunk := pes1
	if len(firstChunk) > maxPayload {
		firstChunk = firstChunk[:maxPayload]
	}
	b.emit(testVideoPID, firstChunk, packetOptions{pusi: true})
	rest := pes1[len(firstChunk):]
	midChunk := rest
	if len(midChunk) > maxPayload {
		midChunk = midChunk[:maxPayload]
	}
	b.emit(testVideoPID, midChunk, packetOptions{})
	b.skip(testVideoPID) // 模拟一个丢失的 TS 包
	if len(rest) > maxPayload {
		b.emit(testVideoPID, rest[maxPayload:], packetOptions{})
	}

	// PES2 完好。
	b.emitPES(testVideoPID, pes2)

	rd := NewReader(bytes.NewReader(b.bytes()))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 2 {
		t.Fatalf("expected 2 PES (1 damaged + 1 clean), got %d", len(pes))
	}
	if !pes[0].Corrupted {
		t.Fatal("PES spanning lost packet must be marked corrupted")
	}
	if pes[1].Corrupted || !bytes.Equal(pes[1].Data, payload2) {
		t.Fatal("PES after re-sync must be clean and complete")
	}

	stats := rd.Stats()
	if stats.ContinuityErrors != 1 || stats.CorruptedPES != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	corruptions := rd.Corruptions()
	if len(corruptions) != 1 || corruptions[0].Kind != CorruptContinuity ||
		corruptions[0].PID != testVideoPID {
		t.Fatalf("unexpected corruption record: %+v", corruptions)
	}
}

// 起始包即丢失：没有 PUSI 的数据到达时开始组装并标记受损，下一条 PES 恢复。
func TestLostStartPacket(t *testing.T) {
	b := newDemuxedStream(t)

	payload := bytes.Repeat([]byte{0x33}, 200)
	pes := buildPES(0xE0, 5000, 0, false, payload)

	const maxPayload = PacketSize - 4
	firstChunk := pes
	if len(firstChunk) > maxPayload {
		firstChunk = firstChunk[:maxPayload]
	}
	// 丢弃起始包，只发送后续中间包。
	b.skip(testVideoPID)
	for remaining := pes[len(firstChunk):]; len(remaining) > 0; {
		chunk := remaining
		if len(chunk) > maxPayload {
			chunk = chunk[:maxPayload]
		}
		b.emit(testVideoPID, chunk, packetOptions{})
		remaining = remaining[len(chunk):]
	}

	cleanPES := buildPES(0xE0, 9000, 0, false, bytes.Repeat([]byte{0x44}, 50))
	b.emitPES(testVideoPID, cleanPES)

	rd := NewReader(bytes.NewReader(b.bytes()))
	pesOut := pesEvents(drain(t, rd))
	if len(pesOut) != 2 || !pesOut[0].Corrupted || pesOut[1].Corrupted {
		t.Fatalf("expected damaged orphan PES then clean PES, got %+v", pesOut)
	}
}

// 重复包：相同 CC 的重复载荷被跳过，不产生 CC 错误，PES 数据保持正确。
func TestDuplicatePacketsIgnored(t *testing.T) {
	b := newDemuxedStream(t)

	payload := bytes.Repeat([]byte{0x55}, 250)
	pes := buildPES(0xE0, 7000, 0, false, payload)

	const maxPayload = PacketSize - 4
	firstChunk := pes[:maxPayload]
	firstPkt := b.emit(testVideoPID, firstChunk, packetOptions{pusi: true})
	b.emitRaw(firstPkt) // 原封不动重复一次
	b.emit(testVideoPID, pes[maxPayload:], packetOptions{})

	rd := NewReader(bytes.NewReader(b.bytes()))
	pesOut := pesEvents(drain(t, rd))
	if len(pesOut) != 1 {
		t.Fatalf("expected 1 PES, got %d", len(pesOut))
	}
	if !bytes.Equal(pesOut[0].Data, payload) || pesOut[0].Corrupted {
		t.Fatal("duplicate packet must be ignored and PES stay intact")
	}
	stats := rd.Stats()
	if stats.DuplicatePackets != 1 || stats.ContinuityErrors != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

// PTS/DTS 33 位回绕：原始值回绕后，展开时间必须保持单调并补上周期。
func TestPTSWrapUnwrappedMonotonic(t *testing.T) {
	b := newDemuxedStream(t)

	nearWrap := mod33 - 45000 // 回绕前 0.5 秒（90kHz）
	afterWrap := uint64(45000)
	b.emitPES(testVideoPID, buildPES(0xE0, nearWrap, nearWrap-9000, true, []byte{1}))
	b.emitPES(testVideoPID, buildPES(0xE0, afterWrap, afterWrap-9000, true, []byte{2}))

	rd := NewReader(bytes.NewReader(b.bytes()))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 2 {
		t.Fatalf("expected 2 PES, got %d", len(pes))
	}
	if pes[1].PTSUnwrapped <= pes[0].PTSUnwrapped {
		t.Fatalf("unwrapped PTS not monotonic: %d -> %d", pes[0].PTSUnwrapped, pes[1].PTSUnwrapped)
	}
	if got := pes[1].PTSUnwrapped; got != nearWrap+90000 {
		t.Fatalf("PTS wrap: got %d, want %d", got, nearWrap+90000)
	}
	if got := pes[1].DTSUnwrapped; got != mod33+afterWrap-9000 {
		t.Fatalf("DTS wrap: got %d, want %d", got, mod33+afterWrap-9000)
	}
	if pes[1].PTS != afterWrap {
		t.Fatalf("raw PTS should remain unmodified, got %d", pes[1].PTS)
	}
}

// PCR 42 位回绕与适配字段：PCR 展开单调，填充字节不影响解析。
func TestPCRWrapAndAdaptationField(t *testing.T) {
	b := newDemuxedStream(t)

	// PCR 由 33 位 base（90kHz）与 9 位 extension（27MHz）组成，
	// 回绕周期为 base 模数 * 300。取边界两侧、300 整数倍的合法 raw。
	pcrMod := mod33 * 300
	nearWrap := pcrMod - 300
	afterWrap := uint64(300)
	pcr1 := nearWrap
	pcr2 := afterWrap
	b.emit(testVideoPID, []byte{0x01}, packetOptions{pusi: true, pcr: &pcr1})
	b.emit(testVideoPID, []byte{0x02}, packetOptions{pcr: &pcr2})
	t.Logf("pcr1=%d pcr2=%d", pcr1, pcr2)

	rd := NewReader(bytes.NewReader(b.bytes()))
	var pcrs []*PCRTimestamp
	for _, event := range drain(t, rd) {
		if event.Type == EventPCR {
			pcrs = append(pcrs, event.PCR)
		}
	}
	if len(pcrs) != 2 {
		t.Fatalf("expected 2 PCR events, got %d", len(pcrs))
	}
	if pcrs[0].Raw != pcr1 || pcrs[1].Raw != pcr2 {
		t.Fatalf("raw PCR mismatch: %+v %+v", pcrs[0], pcrs[1])
	}
	if pcrs[1].Time <= pcrs[0].Time {
		t.Fatalf("unwrapped PCR not monotonic: %d -> %d", pcrs[0].Time, pcrs[1].Time)
	}
	if pcrs[1].Time != modPCR+afterWrap {
		t.Fatalf("PCR wrap: got %d, want %d", pcrs[1].Time, modPCR+afterWrap)
	}

	// 适配字段占满整包（无 payload）时不得报错也不得产生数据事件。
	b2 := newDemuxedStream(t)
	pcrOnly := uint64(12345)
	b2.emit(testVideoPID, nil, packetOptions{pcr: &pcrOnly})
	rd2 := NewReader(bytes.NewReader(b2.bytes()))
	events2 := drain(t, rd2)
	if len(events2) != 1 || events2[0].Type != EventPCR {
		t.Fatalf("adaptation-only packet should yield exactly one PCR event, got %d", len(events2))
	}
}

// PAT/PMT 版本更新：新版本生效后信息被替换，旧版本重复广播不触发重建。
func TestPATPMTVersionUpdate(t *testing.T) {
	b := newDemuxedStream(t)

	// 同版本重复广播，版本号保持不变。
	b.emitSection(PIDPAT, buildPAT(0))
	b.emitSection(testPMTPID, buildPMT(0))
	rd := NewReader(bytes.NewReader(b.bytes()))
	drain(t, rd)
	if rd.Stats().PATVersion != 0 || rd.Stats().PMTVersions[testPMTPID] != 0 {
		t.Fatalf("same-version tables should be no-op: %+v", rd.Stats())
	}

	// 新版本 PAT/PMT。
	b.emitSection(PIDPAT, buildPAT(1))
	b.emitSection(testPMTPID, buildPMT(1))
	b.emitPES(testVideoPID, buildPES(0xE0, 1234, 0, false, []byte{0x77}))
	rd = NewReader(bytes.NewReader(b.bytes()))
	events := drain(t, rd)
	if rd.Stats().PATVersion != 1 || rd.Stats().PMTVersions[testPMTPID] != 1 {
		t.Fatalf("version not updated: %+v", rd.Stats())
	}
	pes := pesEvents(events)
	if len(pes) != 1 || !bytes.Equal(pes[0].Data, []byte{0x77}) {
		t.Fatalf("PES after PMT version update lost: %+v", pes)
	}
	info, ok := rd.Stream(testAudioPID)
	if !ok || info.StreamType != 0x0F {
		t.Fatalf("audio stream info missing after update: %+v", info)
	}
}

// discontinuity_indicator：复位时间基准并标记受损 PES，新时间轴重新展开。
func TestDiscontinuityIndicator(t *testing.T) {
	b := newDemuxedStream(t)

	// 第一条 PES 跨两包。
	pes1 := buildPES(0xE0, 1_000_000, 0, false, bytes.Repeat([]byte{0x66}, 200))
	b.emitPES(testVideoPID, pes1)

	// 不连续点：DI 置位的新 PES 起始包，PTS 跳回很小的值。
	pes2 := buildPES(0xE0, 5000, 0, false, []byte{0x67})
	firstChunk := pes2
	const maxPayload = PacketSize - 4
	if len(firstChunk) > maxPayload {
		firstChunk = firstChunk[:maxPayload]
	}
	b.emit(testVideoPID, firstChunk, packetOptions{pusi: true, discontinuity: true})
	if len(pes2) > maxPayload {
		b.emitPES(testVideoPID, pes2[maxPayload:])
	}

	rd := NewReader(bytes.NewReader(b.bytes()))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 2 {
		t.Fatalf("expected 2 PES, got %d", len(pes))
	}
	if !pes[0].Corrupted {
		t.Fatal("PES before DI boundary must be marked corrupted")
	}
	if pes[1].Corrupted {
		t.Fatal("PES starting at DI boundary must be clean")
	}
	// 时间轴已复位：新 PES 的展开 PTS 等于其原始值，而不是被加上偏移。
	if pes[1].PTSUnwrapped != 5000 {
		t.Fatalf("PTS baseline not reset after DI: got %d", pes[1].PTSUnwrapped)
	}
	if rd.Stats().Discontinuities != 1 {
		t.Fatalf("expected 1 discontinuity, got %+v", rd.Stats())
	}
}

// 流起始处存在杂散字节时，读取器应重新找到同步字节并正常解复用。
func TestResyncToSyncByte(t *testing.T) {
	b := newDemuxedStream(t)
	b.emitPES(testVideoPID, buildPES(0xE0, 321, 0, false, []byte{0x88}))

	garbage := []byte{0x00, 0x11, 0x22, SyncByte ^ 0xFF}
	stream := append(garbage, b.bytes()...)
	rd := NewReader(bytes.NewReader(stream))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 1 || !bytes.Equal(pes[0].Data, []byte{0x88}) {
		t.Fatalf("resync failed, PES output: %+v", pes)
	}
	if rd.Stats().Resyncs == 0 {
		t.Fatalf("resync counter not incremented: %+v", rd.Stats())
	}
}
