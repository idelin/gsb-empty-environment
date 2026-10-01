package verifier

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Tamper selects a deterministic mutation of the synthesized sample bundle,
// used by the demo CLI and the tests.
type Tamper string

const (
	TamperNone      Tamper = "none"      // clean bundle, seals
	TamperReference Tamper = "reference" // one reference points at a nonexistent log record
	TamperDigest    Tamper = "digest"    // one byte of app.log flipped after the manifest was fixed
	TamperMissing   Tamper = "missing"   // metrics.jsonl never arrives
	TamperTimeRange Tamper = "timerange" // manifest declares a too-narrow window for app.log
	TamperEmpty     Tamper = "empty"     // one empty file arrives, another declared-empty file does not
)

var base = time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

func ts(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func span(id string, start, end int) string {
	return fmt.Sprintf(`{"id":%q,"timestamp":%q,"start":%q,"end":%q}`,
		id, ts(start).Format(time.RFC3339Nano), ts(start).Format(time.RFC3339Nano), ts(end).Format(time.RFC3339Nano))
}

func point(id string, at int) string {
	return fmt.Sprintf(`{"id":%q,"timestamp":%q}`, id, ts(at).Format(time.RFC3339Nano))
}

func entry(name string, kind Kind, data []byte, start, end int) FileEntry {
	sum := sha256.Sum256(data)
	return FileEntry{
		Name:   name,
		Kind:   kind,
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(data)),
		TimeRange: TimeRange{
			Start: ts(start),
			End:   ts(end),
		},
	}
}

// Synthesize builds a deterministic evidence bundle: a trace file, a log
// file and a metrics file plus a versioned manifest with two cross-file
// references. It returns the chunks (payloads) and the manifest. Depending
// on the tamper mode the chunk list or manifest is mutated.
func Synthesize(t Tamper) (chunks []Chunk, m Manifest) {
	trace := []byte(span("span-1", 0, 5) + "\n" + span("span-2", 10, 12) + "\n")
	logf := []byte(point("log-1", 2) + "\n" + point("log-2", 11) + "\n")
	metrics := []byte(point("met-1", 3) + "\n" + point("met-2", 9) + "\n")
	empty := []byte{}

	m = Manifest{
		Version: "v1",
		Files: []FileEntry{
			entry("trace.jsonl", KindTrace, trace, 0, 12),
			entry("app.log", KindLog, logf, 0, 12),
			entry("metrics.jsonl", KindMetric, metrics, 0, 12),
		},
		References: []Reference{
			{Source: RecordRef{File: "trace.jsonl", Record: "span-1"}, Target: RecordRef{File: "app.log", Record: "log-1"}},
			{Source: RecordRef{File: "trace.jsonl", Record: "span-2"}, Target: RecordRef{File: "app.log", Record: "log-2"}},
		},
	}

	chunks = []Chunk{
		{Version: m.Version, Name: "trace.jsonl", Data: trace},
		{Version: m.Version, Name: "app.log", Data: logf},
		{Version: m.Version, Name: "metrics.jsonl", Data: metrics},
	}

	switch t {
	case TamperNone:
	case TamperReference:
		// A single tampered reference: span-1 now cites a log record that
		// does not exist. The report must locate span-1 as the source.
		m.References[0].Target.Record = "log-999"
	case TamperDigest:
		// Payload mutated after the manifest digest was fixed.
		tampered := append([]byte(nil), logf...)
		tampered[20] ^= 0xFF
		chunks[1].Data = tampered
	case TamperMissing:
		chunks = chunks[:2] // metrics.jsonl never arrives
	case TamperTimeRange:
		// Manifest window for app.log narrowed so log-2 falls outside.
		for i := range m.Files {
			if m.Files[i].Name == "app.log" {
				m.Files[i].TimeRange.End = ts(4)
			}
		}
	case TamperEmpty:
		// notes.log arrives as a genuine empty file; ghost.log is declared
		// empty too but its chunk never arrives. The two must be reported
		// differently.
		m.Files = append(m.Files,
			entry("notes.log", KindLog, empty, 0, 12),
			entry("ghost.log", KindLog, empty, 0, 12),
		)
		chunks = append(chunks, Chunk{Version: m.Version, Name: "notes.log", Data: empty})
	}
	return chunks, m
}

// Actions converts a synthesized bundle into a replayable fixture.
func Actions(chunks []Chunk, m Manifest) []Action {
	actions := make([]Action, 0, len(chunks)+1)
	for _, c := range chunks {
		actions = append(actions, Stage(c))
	}
	return append(actions, CommitAct(m))
}
