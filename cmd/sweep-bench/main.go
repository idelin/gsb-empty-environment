// sweep-bench 用正弦扫频测量重采样器的通带误差与混叠/镜像抑制。
//
// 用法：
//
//	go run ./cmd/sweep-bench
//
// 默认测量直播管线关注的 44.1kHz/48kHz 互转以及整倍数参考比例，
// 所有结果以分贝打印，数值越负越好。
package main

import (
	"fmt"
	"os"

	"example.org/resample"
)

func main() {
	cases := []struct {
		name string
		cfg  resample.Config
	}{
		{"44.1kHz->48kHz (Blackman, L=32)", resample.Config{
			InRate: 44100, OutRate: 48000, Channels: 1, Window: resample.WindowBlackman}},
		{"48kHz->44.1kHz (Blackman, L=32)", resample.Config{
			InRate: 48000, OutRate: 44100, Channels: 1, Window: resample.WindowBlackman}},
		{"48kHz->24kHz 2:1 (Blackman, L=32)", resample.Config{
			InRate: 48000, OutRate: 24000, Channels: 1, Window: resample.WindowBlackman}},
		{"24kHz->48kHz 1:2 (Blackman, L=32)", resample.Config{
			InRate: 24000, OutRate: 48000, Channels: 1, Window: resample.WindowBlackman}},
		{"48kHz->16kHz 3:1 (Hann, L=16)", resample.Config{
			InRate: 48000, OutRate: 16000, Channels: 1, HalfLength: 16, Window: resample.WindowHann}},
	}

	fmt.Printf("%-38s %14s %14s %14s\n", "case", "passband/dB", "alias/dB", "image/dB")
	failed := false
	for _, tc := range cases {
		rep, err := resample.MeasureSweep(tc.cfg, 8.0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tc.name, err)
			failed = true
			continue
		}
		fmt.Printf("%-38s %14s %14s %14s\n",
			tc.name,
			fmt.Sprintf("%0.2f", rep.PassbandErrorDB),
			na(rep.AliasSuppressionDB),
			na(rep.ImageSuppressionDB))
	}
	if failed {
		os.Exit(1)
	}
}

func na(v float64) string {
	if v != v { // NaN
		return "n/a"
	}
	return fmt.Sprintf("%0.2f", v)
}
