// Command lyrpack demonstrates the layerpack container: layered encoding to
// a file, bounded decoding, and the shared budget protections.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"layerpack"
)

func main() {
	dir, err := os.MkdirTemp("", "lyrpack-demo-*")
	must(err)
	defer os.RemoveAll(dir)
	fmt.Println("workdir:", dir)

	// 1. Layered file round trip: flate + gzip, temp file + atomic rename.
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&sb, "record %06d: the quick brown fox jumps over the lazy dog\n", i)
	}
	sample := []byte(sb.String())
	container := filepath.Join(dir, "sample.lyr")
	must(layerpack.EncodeFile(container, bytes.NewReader(sample),
		[]layerpack.Codec{layerpack.Flate, layerpack.Gzip}, layerpack.DefaultLimits()))
	fi, _ := os.Stat(container)
	fmt.Printf("[roundtrip] raw=%dB container=%dB (2 layers: flate,gzip)\n", len(sample), fi.Size())

	restored := filepath.Join(dir, "restored.txt")
	must(layerpack.DecodeFile(restored, container, layerpack.DefaultLimits()))
	got, _ := os.ReadFile(restored)
	fmt.Printf("[roundtrip] decoded=%dB identical=%v\n", len(got), bytes.Equal(got, sample))

	// 2. Expansion-ratio budget: 4 MiB of zeros compress ~4000x.
	zeros := make([]byte, 4<<20)
	var bomb bytes.Buffer
	must(layerpack.Encode(&bomb, bytes.NewReader(zeros), []layerpack.Codec{layerpack.Flate}, layerpack.DefaultLimits()))
	fmt.Printf("[ratio] 4MiB zeros -> %dB container\n", bomb.Len())
	bombBytes := bytes.Clone(bomb.Bytes())
	var out bytes.Buffer
	err = layerpack.Decode(&out, bytes.NewReader(bombBytes), layerpack.DefaultLimits())
	fmt.Printf("[ratio] default limits:        %v\n", err)
	lim := layerpack.DefaultLimits()
	lim.AllowHighRatio = true // explicit opt-in for legitimate high ratios
	out.Reset()
	err = layerpack.Decode(&out, bytes.NewReader(bombBytes), lim)
	fmt.Printf("[ratio] AllowHighRatio=true:   err=%v decoded=%dB ok=%v\n", err, out.Len(), bytes.Equal(out.Bytes(), zeros))

	// 3. Cumulative output budget: each layer is under the limit alone,
	// but the layers together exceed it.
	var multi bytes.Buffer
	must(layerpack.Encode(&multi, bytes.NewReader(bytes.Repeat([]byte("x"), 60)),
		[]layerpack.Codec{layerpack.Store, layerpack.Store, layerpack.Store}, layerpack.DefaultLimits()))
	lim = layerpack.DefaultLimits()
	lim.MaxOutputBytes = 100 // 60/layer fits alone; 3x60 cumulative does not
	err = layerpack.Decode(&out, &multi, lim)
	fmt.Printf("[budget] 3x60B layers, MaxOutputBytes=100: %v (cumulative blocked)\n", err)
	fmt.Printf("[budget] is ErrBudgetOutput: %v\n", errors.Is(err, layerpack.ErrBudgetOutput))

	// 4. Failed writes clean up temporary output.
	bad := layerpack.DefaultLimits()
	bad.MaxInputBytes = 8
	err = layerpack.EncodeFile(filepath.Join(dir, "nope.lyr"), bytes.NewReader(make([]byte, 4096)),
		[]layerpack.Codec{layerpack.Flate}, bad)
	entries, _ := os.ReadDir(dir)
	fmt.Printf("[cleanup] encode error: %v\n", err)
	fmt.Printf("[cleanup] files left in workdir: %d (only sample.lyr & restored.txt expected)\n", len(entries))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
