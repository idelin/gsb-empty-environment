// hazmat-demo 模拟危险品运输车运行监控：
// 车辆起步加速 -> 直线巡航 -> 经过转弯道（向心加速度超过侧翻阈值）-> 出弯直行。
// GPS 采样 10Hz、定位噪声 σ=0.4m，探测器输出违规事件及前后各 10 秒的完整运动学曲线。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"kinematics"
)

type segment struct {
	dur float64
	pos func(t float64) []float64
}

func straight(dur, v0, a float64, dir []float64) segment {
	return segment{dur, func(t float64) []float64 {
		s := v0*t + 0.5*a*t*t
		return []float64{dir[0] * s, dir[1] * s}
	}}
}

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

func simulate(dt, noise float64, segs ...segment) []kinematics.Sample {
	rng := rand.New(rand.NewSource(7))
	var out []kinematics.Sample
	base := make([]float64, 2)
	var t0 float64
	for _, seg := range segs {
		for i := 0; i < int(seg.dur/dt); i++ {
			lt := float64(i) * dt
			p := seg.pos(lt)
			out = append(out, kinematics.Sample{T: t0 + lt, Pos: []float64{
				p[0] + base[0] + noise*rng.NormFloat64(),
				p[1] + base[1] + noise*rng.NormFloat64(),
			}})
		}
		end := seg.pos(seg.dur)
		base[0] += end[0]
		base[1] += end[1]
		t0 += seg.dur
	}
	return out
}

func main() {
	const v, r, arcDur = 12.0, 25.0, 8.0
	exit := v * arcDur / r
	samples := simulate(0.1, 0.4,
		straight(10, 0, 1.2, []float64{1, 0}),                         // 0-10s   平稳起步加速至 12 m/s
		straight(20, v, 0, []float64{1, 0}),                           // 10-30s  直线巡航
		arc(arcDur, v, r, 0),                                          // 30-38s  转弯道 r=25m，向心加速度 5.76 m/s²
		straight(15, v, 0, []float64{math.Cos(exit), math.Sin(exit)}), // 38-53s 出弯直行
	)

	det := kinematics.NewDetector(kinematics.Config{
		WindowSize:     21, // 2.1s 滑动窗口
		PolyDegree:     2,
		PreEvent:       10, // 证据链：违规前 10s
		PostEvent:      10, // 证据链：违规后 10s
		TriggerSamples: 3,  // 连续 0.3s 违规才立案
		Thresholds: kinematics.Thresholds{
			MaxSpeed:               16.7, // 60 km/h
			MaxAcceleration:        2.5,  // m/s²
			MaxDeceleration:        3.5,  // m/s²
			MaxCentripetal:         4.0,  // 侧翻安全阈值 ≈0.41g
			MinSpeedForCentripetal: 3.0,
		},
		OnEvent: func(e kinematics.Event) { report(e) },
	})
	for _, s := range samples {
		det.Add(s) // 流式送入 GPS 轨迹
	}
	det.Flush()

	fmt.Printf("\n共结案 %d 起违规事件\n", len(det.Events()))
}

func report(e kinematics.Event) {
	fmt.Printf("⚠ 事件 #%d  违规时段 [%.1fs, %.1fs]  类型: %s\n",
		e.ID, e.Start, e.End, strings.Join(typeNames(e), ", "))
	fmt.Printf("  峰值违规: %s\n", e.Peak)
	fmt.Printf("  证据链: %d 个运动学采样点，覆盖 [%.1fs, %.1fs]（违规前后各约 10 秒）\n",
		len(e.Curve), e.Curve[0].T, e.Curve[len(e.Curve)-1].T)
	fmt.Println("  t(s)   速度(m/s)  切向加速度(m/s²)  向心加速度(m/s²)  违规")
	fmt.Println("  ----   ---------  ---------------  ---------------  ----")
	last := -1.0
	for _, p := range e.Curve {
		if p.T-last < 1.0 { // 每秒抽一行展示
			continue
		}
		last = p.T
		fmt.Printf("  %5.1f  %9.2f  %15.2f  %15.2f  %s\n",
			p.T, p.Speed, p.Tangential, p.Centripetal, flagsAt(e, p.T))
	}
}

func typeNames(e kinematics.Event) []string {
	var out []string
	for _, ty := range e.Types() {
		out = append(out, string(ty))
	}
	return out
}

func flagsAt(e kinematics.Event, t float64) string {
	var flags []string
	for _, v := range e.Violations {
		if math.Abs(v.T-t) < 0.05 {
			flags = append(flags, string(v.Type))
		}
	}
	return strings.Join(flags, ",")
}
