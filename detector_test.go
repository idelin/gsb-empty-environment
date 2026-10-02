package kinematics

import (
	"math"
	"math/rand"
	"testing"
)

// ---------- 轨迹仿真工具 ----------

// segment 描述一段理想运动：返回 t 时刻（段内秒）位置。
type segment struct {
	dur float64
	pos func(t float64) []float64
}

// simulate 按段拼接理想轨迹，采样周期 dt，叠加高斯噪声，返回采样流。
func simulate(dt, noise float64, segs ...segment) []Sample {
	rng := rand.New(rand.NewSource(42))
	var out []Sample
	base := segs[0].pos(0)
	for d := range base {
		base[d] = 0
	}
	var t0 float64
	for _, seg := range segs {
		n := int(seg.dur / dt)
		for i := 0; i < n; i++ {
			lt := float64(i) * dt
			p := seg.pos(lt)
			pos := make([]float64, len(p))
			for d := range p {
				pos[d] = p[d] + base[d] + noise*rng.NormFloat64()
			}
			out = append(out, Sample{T: t0 + lt, Pos: pos})
		}
		end := seg.pos(seg.dur)
		for d := range end {
			base[d] += end[d]
		}
		t0 += seg.dur
	}
	return out
}

// straight 匀加速直线段：起点原点，初速 v0，加速度 a，方向 dir（单位向量）。
func straight(dur, v0, a float64, dir []float64) segment {
	return segment{dur, func(t float64) []float64 {
		s := v0*t + 0.5*a*t*t
		return []float64{dir[0] * s, dir[1] * s}
	}}
}

// arc 半径 r、切向速率 v 的圆弧段，entry 为入弧航向角（rad），
// 圆心在行进方向左侧，向心加速度恒为 v²/r。出弧航向为 entry+v*dur/r。
func arc(dur, v, r, entry float64) segment {
	omega := v / r
	return segment{dur, func(t float64) []float64 {
		th := omega * t
		x := r * math.Sin(th)
		y := r * (1 - math.Cos(th))
		c, s := math.Cos(entry), math.Sin(entry)
		return []float64{c*x - s*y, s*x + c*y}
	}}
}

// heading 返回航向角对应的单位向量。
func heading(theta float64) []float64 {
	return []float64{math.Cos(theta), math.Sin(theta)}
}

func runStream(samples []Sample, cfg Config) []Event {
	d := NewDetector(cfg)
	for _, s := range samples {
		d.Add(s)
	}
	d.Flush()
	return d.Events()
}

func testConfig() Config {
	return Config{
		WindowSize:     21, // 2.1s @10Hz
		PolyDegree:     2,
		PreEvent:       10,
		PostEvent:      10,
		TriggerSamples: 3, // 连续 0.3s 违规才立案，抑制单点噪声毛刺
		Thresholds: Thresholds{
			MaxSpeed:               16.7, // 60 km/h
			MaxAcceleration:        2.5,
			MaxDeceleration:        3.5,
			MaxCentripetal:         4.0, // 侧翻安全阈值 ≈0.41g
			MinSpeedForCentripetal: 3.0,
		},
	}
}

// ---------- 测试 ----------

func TestPolyFitExact(t *testing.T) {
	// y = 3 - 2x + 0.5x² 应被二阶拟合精确还原
	var xs, ys []float64
	for i := -10; i <= 10; i++ {
		x := float64(i) / 10
		xs = append(xs, x)
		ys = append(ys, 3-2*x+0.5*x*x)
	}
	c := polyFit(xs, ys, 2)
	want := []float64{3, -2, 0.5}
	for k := range want {
		if math.Abs(c[k]-want[k]) > 1e-9 {
			t.Fatalf("coef[%d] = %v, want %v", k, c[k], want[k])
		}
	}
}

func TestSmoothsGPSNoise(t *testing.T) {
	// 匀速直线运动 v=(3,4) m/s，叠加 σ=0.3m 的 GPS 噪声
	samples := simulate(0.1, 0.3, straight(60, 5, 0, []float64{0.6, 0.8}))
	diff := newDifferentiator(21, 2)
	half := diff.window / 2
	var maxSpeedErr, maxAccel float64
	for i := half; i < len(samples)-half; i++ {
		p := diff.eval(samples[i-half:i+half+1], half)
		if e := math.Abs(p.Speed - 5); e > maxSpeedErr {
			maxSpeedErr = e
		}
		if p.Centripetal > maxAccel {
			maxAccel = p.Centripetal
		}
		if a := math.Abs(p.Tangential); a > maxAccel {
			maxAccel = a
		}
	}
	if maxSpeedErr > 0.5 {
		t.Fatalf("speed estimate error %.3f m/s exceeds 0.5", maxSpeedErr)
	}
	if maxAccel > 1.5 {
		t.Fatalf("spurious acceleration %.3f m/s² exceeds 1.5", maxAccel)
	}
	t.Logf("max speed err %.4f m/s, max spurious accel %.4f m/s²", maxSpeedErr, maxAccel)
}

// 验收场景：危险品运输车经过转弯道，瞬时向心加速度超过侧翻阈值，
// 证据链须覆盖违规前后各 10 秒的完整运动学曲线。
func TestCurveRolloverDetection(t *testing.T) {
	const v, r = 12.0, 25.0 // 向心加速度 v²/r = 5.76 m/s² > 4.0 阈值
	const arcDur = 8.0
	exit := v * arcDur / r // 出弧航向角
	samples := simulate(0.1, 0.4,
		straight(10, 0, 1.2, []float64{1, 0}), // 平稳起步加速到 12 m/s
		straight(20, v, 0, []float64{1, 0}),   // 直线巡航
		arc(arcDur, v, r, 0),                  // 转弯道：8 秒圆弧
		straight(15, v, 0, heading(exit)),     // 出弯后沿切线继续直行
	)
	events := runStream(samples, testConfig())
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %v", len(events), events)
	}
	ev := events[0]

	// 事件类型须包含向心加速度超限，且无超速/急加减速误报
	types := ev.Types()
	if len(types) != 1 || types[0] != ExcessiveCentripetal {
		t.Fatalf("event types = %v, want [excessive_centripetal]", types)
	}

	// 违规期间向心加速度均值应接近理论值 v²/r（峰值含噪声正向偏置，
	// 只需明显高于阈值且不超过理论值+噪声包络）
	theory := v * v / r
	var sum float64
	var nv int
	for _, viol := range ev.Violations {
		if viol.Type == ExcessiveCentripetal {
			sum += viol.Value
			nv++
		}
	}
	mean := sum / float64(nv)
	if math.Abs(mean-theory) > 0.4 {
		t.Fatalf("mean centripetal %.2f, theory %.2f", mean, theory)
	}
	if ev.Peak.Type != ExcessiveCentripetal {
		t.Fatalf("peak type = %v", ev.Peak.Type)
	}
	if ev.Peak.Value < theory || ev.Peak.Value > theory+1.5 {
		t.Fatalf("peak centripetal %.2f out of [%.2f, %.2f]", ev.Peak.Value, theory, theory+1.5)
	}

	// 证据链完整性：覆盖首次违规前 ≥10s、最后一次违规后 ≥10s
	curve := ev.Curve
	if len(curve) == 0 {
		t.Fatal("empty evidence curve")
	}
	if head := ev.Start - curve[0].T; head < 9.5 || head > 10.5 {
		t.Fatalf("pre-event coverage %.2fs, want ~10s", head)
	}
	if tail := curve[len(curve)-1].T - ev.End; tail < 9.5 || tail > 10.5 {
		t.Fatalf("post-event coverage %.2fs, want ~10s", tail)
	}

	// 证据链曲线中，弯道中段测得的向心加速度应稳定高于阈值
	var midOK bool
	for _, p := range curve {
		if p.T > ev.Start+2 && p.T < ev.End-2 && p.Centripetal > 4.5 {
			midOK = true
		}
	}
	if !midOK {
		t.Fatal("mid-curve centripetal acceleration not stably above threshold")
	}
	t.Logf("event #%d [%.1f, %.1f]s mean=%.2f peak=%.2f m/s² (theory %.2f), evidence %d points [%.1f, %.1f]s",
		ev.ID, ev.Start, ev.End, mean, ev.Peak.Value, theory, len(curve), curve[0].T, curve[len(curve)-1].T)
}

func TestHarshBraking(t *testing.T) {
	samples := simulate(0.1, 0.2,
		straight(15, 15, 0, []float64{1, 0}),
		straight(3, 15, -5, []float64{1, 0}), // -5 m/s² 急刹车，阈值 3.5
		straight(15, 0, 0, []float64{1, 0}),
	)
	events := runStream(samples, testConfig())
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	for _, ty := range events[0].Types() {
		if ty != HarshBraking {
			t.Fatalf("unexpected violation type %v", ty)
		}
	}
	if events[0].Peak.Value < 4.5 || events[0].Peak.Value > 5.5 {
		t.Fatalf("peak deceleration %.2f, want ~5.0", events[0].Peak.Value)
	}
}

func TestOverspeed(t *testing.T) {
	samples := simulate(0.1, 0.2,
		straight(20, 0, 1.0, []float64{1, 0}), // 平稳加速到 20 m/s > 16.7 阈值
		straight(15, 20, 0, []float64{1, 0}),
	)
	events := runStream(samples, testConfig())
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	types := events[0].Types()
	if len(types) != 1 || types[0] != Overspeed {
		t.Fatalf("event types = %v, want [overspeed]", types)
	}
	if math.Abs(events[0].Peak.Value-20) > 0.3 {
		t.Fatalf("peak speed %.2f, want ~20", events[0].Peak.Value)
	}
}

func TestNormalDrivingNoFalsePositive(t *testing.T) {
	// 正常驾驶：平稳加减速 + 大半径弯道（向心加速度低于阈值），含 GPS 噪声
	exit := 12.0 * 10 / 100
	samples := simulate(0.1, 0.4,
		straight(12, 0, 1.0, []float64{1, 0}),
		straight(20, 12, 0, []float64{1, 0}),
		arc(10, 12, 100, 0), // 向心加速度 1.44 m/s²，安全
		straight(10, 12, -1.2, heading(exit)),
	)
	if events := runStream(samples, testConfig()); len(events) != 0 {
		t.Fatalf("false positives: %v", events)
	}
}

func TestStreamingCallbackAndFlush(t *testing.T) {
	var fired []Event
	cfg := testConfig()
	cfg.OnEvent = func(e Event) { fired = append(fired, e) }
	d := NewDetector(cfg)
	for _, s := range simulate(0.1, 0.2,
		straight(20, 0, 1.0, []float64{1, 0}),
		straight(15, 20, 0, []float64{1, 0}),
	) {
		d.Add(s)
	}
	d.Flush()
	if len(fired) != 1 || len(d.Events()) != 1 {
		t.Fatalf("callback fired %d times, events %d", len(fired), len(d.Events()))
	}
}
