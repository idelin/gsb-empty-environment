package covernorm

import "fmt"

// Explain renders the complete merged timeline as deterministic, human
// readable lines. Each line shows the sequence number, event kind, raw
// addresses where relevant, and the normalized attribution or failure reason,
// so reloads, reordering and cross-module jumps can be audited end to end.
func (a *Archive) Explain() []string {
	lines := make([]string, 0, len(a.Timeline))
	for _, entry := range a.Timeline {
		lines = append(lines, explainEntry(entry))
	}
	return lines
}

func explainEntry(entry TimelineEntry) string {
	switch entry.Kind {
	case EntryLoad:
		return fmt.Sprintf("#%-3d load      gen=%d module=%s digest=%s segments=%s",
			entry.Seq, entry.Load.Generation, entry.Load.Module,
			entry.Load.Digest.Short(), explainSegments(entry.Load.Segments))

	case EntryUnload:
		return fmt.Sprintf("#%-3d unload    module=%s digest=%s",
			entry.Seq, entry.Unload.Module, entry.Unload.Digest.Short())

	case EntryEdge:
		return fmt.Sprintf("#%-3d edge      thread=%-2d %#08x->%#08x => %s",
			entry.Seq, entry.Thread, entry.FromRaw, entry.ToRaw, entry.Edge)

	default:
		unknown := entry.Unknown
		return fmt.Sprintf("#%-3d unknown   thread=%-2d %#08x->%#08x from=%s to=%s",
			entry.Seq, entry.Thread, entry.FromRaw, entry.ToRaw,
			unknown.FromReason, unknown.ToReason)
	}
}

func explainSegments(segments []Segment) string {
	if len(segments) == 0 {
		return "-"
	}
	text := segments[0].String()
	for _, segment := range segments[1:] {
		text += "," + segment.String()
	}
	return text
}
