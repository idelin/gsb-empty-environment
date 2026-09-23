// Command fcdemo compresses files into independent frames and extracts them
// back, tolerating corrupted regions. It also provides a self-contained
// "demo" command that corrupts an archive on purpose and shows recovery.
//
// Usage:
//
//	fcdemo compress <in> <out>   compress in 4 KiB independent frames
//	fcdemo extract <in> <out>    decode, skipping corrupted frames
//	fcdemo demo                  corrupt-and-recover demonstration
package main

import (
	"bytes"
	"fmt"
	"os"

	"framecomp"
)

const blockSize = 4096

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "compress":
		err = cmdCompress(os.Args[2], os.Args[3])
	case "extract":
		err = cmdExtract(os.Args[2], os.Args[3])
	case "demo":
		err = cmdDemo()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fcdemo:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fcdemo compress <in> <out> | extract <in> <out> | demo")
}

func cmdCompress(inPath, outPath string) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	var buf []byte
	frames := 0
	for off := 0; off < len(data); off += blockSize {
		end := off + blockSize
		if end > len(data) {
			end = len(data)
		}
		buf = framecomp.AppendFrame(buf, data[off:end])
		frames++
	}
	if err := os.WriteFile(outPath, buf, 0o644); err != nil {
		return err
	}
	fmt.Printf("compressed %d bytes into %d frames (%d bytes)\n", len(data), frames, len(buf))
	return nil
}

func cmdExtract(inPath, outPath string) error {
	data, err := os.ReadFile(inPath)
	if err != nil {
		return err
	}
	out, st, err := framecomp.DecodeAll(data, framecomp.DefaultLimits())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v (recovered what was possible)\n", err)
	}
	if werr := os.WriteFile(outPath, out, 0o644); werr != nil {
		return werr
	}
	fmt.Printf("decoded %d frames, %d bytes out; skipped %d bytes, rejected %d bad candidates\n",
		st.Frames, st.OutputBytes, st.SkippedBytes, st.BadCandidates)
	return nil
}

func cmdDemo() error {
	blocks := [][]byte{
		bytes.Repeat([]byte("alpha frame: the quick brown fox. "), 40),
		bytes.Repeat([]byte("BETA frame: this one gets corrupted. "), 40),
		bytes.Repeat([]byte("gamma frame: survives the damage. "), 40),
	}
	var archive []byte
	for _, b := range blocks {
		archive = framecomp.AppendFrame(archive, b)
	}
	fmt.Printf("archive: 3 frames, %d bytes\n", len(archive))

	// Damage 1: flip 64 bytes inside frame 2's payload.
	off := bytes.Index(archive, []byte("BETA"))
	if off < 0 {
		return fmt.Errorf("demo setup: frame 2 payload not found")
	}
	for i := off; i < off+64 && i < len(archive); i++ {
		archive[i] ^= 0xFF
	}
	// Damage 2: plant a fake header claiming a huge compressed length.
	fake := []byte{'F', 'C', 'M', 'P', 1, 1, 0xFF, 0xFF, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0x7F, 0, 0, 0, 0}
	mid := len(archive) / 2
	archive = append(archive[:mid], append(fake, archive[mid:]...)...)

	out, st, err := framecomp.DecodeAll(archive, framecomp.DefaultLimits())
	fmt.Printf("decode err=%v frames=%d out=%d skipped=%d bad=%d\n",
		err, st.Frames, st.OutputBytes, st.SkippedBytes, st.BadCandidates)
	want := append(append([]byte{}, blocks[0]...), blocks[2]...)
	if !bytes.Equal(out, want) {
		return fmt.Errorf("recovery mismatch: got %d bytes, want %d", len(out), len(want))
	}
	fmt.Println("recovered frames 1 and 3 exactly; corrupted frame 2 emitted nothing")
	return nil
}
