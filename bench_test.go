package tmplmatch

import (
	"image"
	"testing"
)

func benchScene() (*GrayImage, *GrayImage) {
	scene, tmpl := makeScene(640, 480, 32, 32,
		[]image.Point{{X: 120, Y: 110}}, 1, 20261111)
	sg := ToGray(scene, false)
	tg := ToGray(tmpl, true)
	return sg, tg
}

func BenchmarkScanBrute(b *testing.B) {
	sg, tg := benchScene()
	ts := buildTemplateStats(tg)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		c := scanBrute(sg, tg, ts, 1, 0.8, 512)
		if len(c) == 0 {
			b.Fatal("no candidates")
		}
	}
}

func BenchmarkScanIntegral(b *testing.B) {
	sg, tg := benchScene()
	ts := buildTemplateStats(tg)
	ii := NewIntegral(sg)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		c := scanIntegral(sg, ii, tg, ts, 1, 0.8, 512)
		if len(c) == 0 {
			b.Fatal("no candidates")
		}
	}
}
