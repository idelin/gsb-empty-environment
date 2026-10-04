// Command evidence-demo synthesizes a trace/log/metrics evidence
// package locally, feeds it to the verifier out of order, and prints
// the verification report plus the synthesized telemetry events.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"evidence"
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func main() {
	base := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	ts := func(min int) string { return base.Add(time.Duration(min) * time.Minute).Format(time.RFC3339Nano) }

	logData := []byte(fmt.Sprintf(
		"L1|%s|boot ok\nL2|%s|request handled\nL3|%s|shutdown\n", ts(0), ts(5), ts(9)))
	traceData := []byte(fmt.Sprintf(
		"T1|%s|span start ref=app.log:L1\nT2|%s|span end ref=app.log:L2\n", ts(1), ts(6)))
	metricsData := []byte(fmt.Sprintf("M1|%s|cpu=0.42\n", ts(2)))
	emptyData := []byte{}

	start, end := base, base.Add(10*time.Minute)
	entry := func(name string, kind evidence.Kind, data []byte) evidence.FileEntry {
		return evidence.FileEntry{Name: name, Kind: kind, Size: int64(len(data)),
			SHA256: digest(data), Start: start, End: end}
	}
	manifest := evidence.Manifest{Version: "v1", Files: []evidence.FileEntry{
		entry("app.log", evidence.KindLog, logData),
		entry("trace.jsonl", evidence.KindTrace, traceData),
		entry("metrics.prom", evidence.KindMetrics, metricsData),
		{Name: "notes.txt", Kind: evidence.KindLog, Size: 0, SHA256: digest(emptyData), Start: start, End: end},
	}}

	dir, err := os.MkdirTemp("", "evidence-store")
	must(err)
	defer os.RemoveAll(dir)
	v, err := evidence.NewVerifier(dir)
	must(err)

	fmt.Println("== scenario A: clean package, out-of-order chunk arrival ==")
	must(v.AcceptManifest(manifest))
	// Deliver chunks in scrambled order; the log arrives in two pieces.
	half := len(logData) / 2
	v.AcceptChunk("v1", "metrics.prom", 0, metricsData)
	v.AcceptChunk("v1", "app.log", int64(half), logData[half:])
	v.AcceptChunk("v1", "trace.jsonl", 0, traceData)
	v.AcceptChunk("v1", "app.log", 0, logData[:half])
	printReport(v.Verify("v1"))

	fmt.Println("== scenario B: tampered digest, dangling reference, missing file ==")
	badTrace := []byte(fmt.Sprintf("T9|%s|span ref=app.log:NOPE\n", ts(3)))
	m2 := evidence.Manifest{Version: "v2", Files: []evidence.FileEntry{
		entry("app.log", evidence.KindLog, logData),
		entry("trace.jsonl", evidence.KindTrace, badTrace),
		entry("metrics.prom", evidence.KindMetrics, metricsData),
	}}
	must(v.AcceptManifest(m2))
	tampered := bytes.Replace(logData, []byte("boot ok"), []byte("BOOTED!"), 1) // same length, wrong digest
	v.AcceptChunk("v2", "app.log", 0, tampered)
	v.AcceptChunk("v2", "app.log", 0, logData) // retransmission after the bad digest
	v.AcceptChunk("v2", "trace.jsonl", 0, badTrace)
	// metrics.prom deliberately never sent -> missing file.
	printReport(v.Verify("v2"))

	fmt.Println("== telemetry (locally synthesized, replayable via fixture) ==")
	var buf bytes.Buffer
	must(v.Telemetry().SaveFixture(&buf))
	replayed, err := evidence.ReplayFixture(&buf)
	must(err)
	for _, ev := range replayed {
		fmt.Printf("#%02d %-18s %s\n", ev.Seq, ev.Type, ev.Detail)
	}
}

func printReport(r evidence.Report) {
	fmt.Printf("version=%s ok=%v\n", r.Version, r.OK())
	for _, f := range r.Findings {
		fmt.Println("  " + f.String())
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
