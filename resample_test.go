package resample

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

func baseConfig(in, out int) Config {
	return Config{InRate: in, OutRate: out, Channels: 1}
}

// interleave 把若干单声道流交织成多声道帧。
func interleave(chans ...[]float64) []float64 {
	n := len(chans[0])
	out := make([]float64, n*len(chans))
	for i := 0; i < n; i++ {
		for c, ch := range chans {
			out[i*len(chans)+c] = ch[i]
		}
	}
	return out
}

func deinterleave(in []float64, channels int) [][]float64 {
	frames := len(in) / channels
	out := make([][]float64, channels)
	for c := range out {
		out[c] = make([]float64, frames)
	}
	for f := 0; f < frames; f++ {
		for c := 0; c < channels; c++ {
			out[c][f] = in[f*channels+c]
		}
	}
	return out
}

func testSignal(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		// 低通有色噪声，能量集中在通带内。
		out[i] = 0.6*math.Sin(2*math.Pi*440*float64(i)/44100) +
			0.3*math.Sin(2*math.Pi*1200*float64(i)/44100) +
			0.1*rng.NormFloat64()
	}
	return out
}

// TestChunkedMatchesWhole 验证任意分块方式（模拟网络抖动）与整段一次
// 处理的输出逐样本完全一致，包含 Drain 尾段。
func TestChunkedMatchesWhole(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"44100_to_48000", baseConfig(44100, 48000)},
		{"48000_to_44100", baseConfig(48000, 44100)},
		{"48000_to_32000", baseConfig(48000, 32000)},
		{"16000_to_48000", baseConfig(16000, 48000)},
		{"same_rate", baseConfig(48000, 48000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := testSignal(4096, 7)

			whole, err := New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]float64(nil), whole.Process(sig)...)
			want = append(want, whole.Drain()...)

			chunked, err := New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			var got []float64
			rng := rand.New(rand.NewSource(99))
			for pos := 0; pos < len(sig); {
				step := 1 + rng.Intn(37)
				end := pos + step
				if end > len(sig) {
					end = len(sig)
				}
				got = append(got, chunked.Process(sig[pos:end])...)
				pos = end
			}
			got = append(got, chunked.Drain()...)

			if len(got) != len(want) {
				t.Fatalf("length mismatch: chunked=%d whole=%d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sample %d differs: chunked=%v whole=%v", i, got[i], want[i])
				}
			}
		})
	}
}

// TestMultichannelIndependent 验证多声道交织处理与逐声道独立处理一致。
func TestMultichannelIndependent(t *testing.T) {
	const channels = 6
	cfg := Config{InRate: 44100, OutRate: 48000, Channels: channels}
	mono := make([]Config, channels)
	for c := range mono {
		mono[c] = baseConfig(44100, 48000)
	}

	perCh := make([][]float64, channels)
	for c := range perCh {
		perCh[c] = testSignal(3000, int64(c+1))
	}
	in := interleave(perCh...)

	multi, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := multi.Process(in)
	got = append(got, multi.Drain()...)

	monoOut := make([][]float64, channels)
	for c := 0; c < channels; c++ {
		r, err := New(mono[c])
		if err != nil {
			t.Fatal(err)
		}
		monoOut[c] = append(r.Process(perCh[c]), r.Drain()...)
	}

	frames := len(got) / channels
	for c := 0; c < channels; c++ {
		if frames != len(monoOut[c]) {
			t.Fatalf("channel %d length mismatch", c)
		}
	}
	gotChans := deinterleave(got, channels)
	for c := 0; c < channels; c++ {
		for i := range monoOut[c] {
			if gotChans[c][i] != monoOut[c][i] {
				t.Fatalf("channel %d sample %d differs", c, i)
			}
		}
	}
}

// TestDrainFlushesTail 验证在数据中途 Drain 的结果，等于只喂入相同前缀
// 数据的独立管线整段处理（含 Drain），且 Drain 后实例可继续处理新流。
func TestDrainFlushesTail(t *testing.T) {
	cfg := baseConfig(44100, 48000)
	sig := testSignal(2000, 3)

	split := 1500
	ref, _ := New(cfg)
	full := append(ref.Process(sig[:split]), ref.Drain()...)

	r, _ := New(cfg)
	head := r.Process(sig[:split])
	tail := r.Drain()
	combined := append(head, tail...)

	if len(combined) != len(full) {
		t.Fatalf("drain output length %d != reference %d", len(combined), len(full))
	}
	for i := range full {
		if combined[i] != full[i] {
			t.Fatalf("sample %d mismatch after drain", i)
		}
	}

	// Drain 后状态复位，继续处理应等价于全新实例。
	fresh, _ := New(cfg)
	freshOut := append(fresh.Process(sig[:100]), fresh.Drain()...)
	reusedOut := append(r.Process(sig[:100]), r.Drain()...)
	if len(reusedOut) != len(freshOut) {
		t.Fatalf("reused resampler length mismatch")
	}
	for i := range freshOut {
		if reusedOut[i] != freshOut[i] {
			t.Fatalf("reused resampler sample %d mismatch", i)
		}
	}
}

// TestSwitchRate 验证流中途切换采样率时先排空旧滤波器，再以新配置
// 继续，两段输出分别等于独立管线的结果。
func TestSwitchRate(t *testing.T) {
	cfg44 := baseConfig(44100, 48000)
	cfg48 := baseConfig(48000, 32000)

	stream44 := testSignal(1800, 11)
	stream48 := testSignal(1500, 22)

	// 参考：两条独立管线。
	refA, _ := New(cfg44)
	wantA := append(refA.Process(stream44), refA.Drain()...)
	refB, _ := New(cfg48)
	wantB := append(refB.Process(stream48), refB.Drain()...)

	r, _ := New(cfg44)
	gotA := r.Process(stream44)
	tail, err := r.SwitchRate(cfg48)
	if err != nil {
		t.Fatal(err)
	}
	gotA = append(gotA, tail...)
	gotB := append(r.Process(stream48), r.Drain()...)

	if len(gotA) != len(wantA) || len(gotB) != len(wantB) {
		t.Fatalf("switch output lengths differ: A %d/%d B %d/%d",
			len(gotA), len(wantA), len(gotB), len(wantB))
	}
	for i := range wantA {
		if gotA[i] != wantA[i] {
			t.Fatalf("pre-switch sample %d mismatch", i)
		}
	}
	for i := range wantB {
		if gotB[i] != wantB[i] {
			t.Fatalf("post-switch sample %d mismatch", i)
		}
	}
}

// TestConfigValidation 覆盖非法配置与默认值填充。
func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{InRate: 0, OutRate: 48000, Channels: 1},
		{InRate: 48000, OutRate: -1, Channels: 1},
		{InRate: 48000, OutRate: 44100, Channels: 0},
		{InRate: 48000, OutRate: 44100, Channels: 1, HalfLength: -1},
		{InRate: 48000, OutRate: 44100, Channels: 1, CutoffHz: 30000},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("case %d expected error", i)
		}
	}

	r, err := New(Config{InRate: 44100, OutRate: 48000, Channels: 2})
	if err != nil {
		t.Fatal(err)
	}
	if r.cfg.HalfLength != defaultHalfLength || r.cfg.Phases != defaultPhases {
		t.Fatalf("defaults not applied: %+v", r.cfg)
	}
	if len(r.filter) != defaultPhases || len(r.filter[0]) != 2*defaultHalfLength {
		t.Fatalf("polyphase table has wrong dimensions")
	}
}

// TestSweepQuality 用正弦扫频测量通带误差与混叠/镜像抑制。
func TestSweepQuality(t *testing.T) {
	cases := []struct {
		name             string
		cfg              Config
		passbandMaxDB    float64
		suppressionMinDB float64 // 抑制量至少达到的分贝数（指标为负，比较 <= -该值）
	}{
		{
			name:             "downsample_2to1_blackman",
			cfg:              Config{InRate: 48000, OutRate: 24000, Channels: 1, Window: WindowBlackman},
			passbandMaxDB:    -60,
			suppressionMinDB: 60,
		},
		{
			name:             "upsample_1to2_blackman",
			cfg:              Config{InRate: 24000, OutRate: 48000, Channels: 1, Window: WindowBlackman},
			passbandMaxDB:    -60,
			suppressionMinDB: 60,
		},
		{
			name:             "downsample_3to1_hann_short",
			cfg:              Config{InRate: 48000, OutRate: 16000, Channels: 1, HalfLength: 16, Window: WindowHann},
			passbandMaxDB:    -45,
			suppressionMinDB: 30,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := MeasureSweep(tc.cfg, 8.0)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: passband=%0.2f dB alias=%v dB image=%v dB",
				tc.name, rep.PassbandErrorDB,
				dbOrInf(rep.AliasSuppressionDB), dbOrInf(rep.ImageSuppressionDB))

			if rep.PassbandErrorDB > tc.passbandMaxDB {
				t.Fatalf("passband error %0.2f dB worse than %0.2f dB",
					rep.PassbandErrorDB, tc.passbandMaxDB)
			}
			supp := rep.AliasSuppressionDB
			if math.IsNaN(supp) {
				supp = rep.ImageSuppressionDB
			}
			if math.IsNaN(supp) || supp > -tc.suppressionMinDB {
				t.Fatalf("suppression %0.2f dB weaker than -%0.2f dB",
					supp, tc.suppressionMinDB)
			}
		})
	}
}

func dbOrInf(v float64) string {
	if math.IsNaN(v) {
		return "n/a"
	}
	return formatDB(v)
}

func formatDB(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// TestHistoryRetention 验证段内历史随输入保留以保证逐样本一致，
// 而 Drain 后历史被清空，内存不会跨流段累积。
func TestHistoryBounded(t *testing.T) {
	cfg := baseConfig(44100, 48000)
	r, _ := New(cfg)
	sig := testSignal(100, 5)
	for i := 0; i < 500; i++ {
		r.Process(sig)
	}
	want := 500 * len(sig)
	for c, h := range r.hist {
		if len(h) != want {
			t.Fatalf("channel %d retained %d samples, want %d", c, len(h), want)
		}
	}

	r.Drain()
	for c, h := range r.hist {
		if len(h) != 0 {
			t.Fatalf("channel %d history not reset after drain: %d", c, len(h))
		}
	}
}

// TestRepeatedSegments 验证多个流段（每段以 Drain 结束）在随机抖动
// 分块下，每段都与独立整段管线逐样本一致，且段间状态完全隔离。
func TestRepeatedSegments(t *testing.T) {
	cfg := baseConfig(44100, 48000)
	rng := rand.New(rand.NewSource(2026))
	r, _ := New(cfg)

	for seg := 0; seg < 4; seg++ {
		sig := testSignal(800+seg*137, int64(seg*31+1))

		ref, _ := New(cfg)
		want := append(ref.Process(sig), ref.Drain()...)

		var got []float64
		for pos := 0; pos < len(sig); {
			step := 1 + rng.Intn(53)
			end := pos + step
			if end > len(sig) {
				end = len(sig)
			}
			got = append(got, r.Process(sig[pos:end])...)
			pos = end
		}
		got = append(got, r.Drain()...)

		if len(got) != len(want) {
			t.Fatalf("segment %d length %d != %d", seg, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("segment %d sample %d mismatch", seg, i)
			}
		}
		if r.nIn != 0 || r.acc != 0 {
			t.Fatalf("segment %d state not reset after drain", seg)
		}
	}
}
