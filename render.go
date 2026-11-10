package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func render(rep *report, format string) ([]byte, error) {
	switch format {
	case "", "json":
		return json.MarshalIndent(rep, "", "  ")
	case "text":
		return []byte(renderText(rep)), nil
	default:
		return nil, fmt.Errorf("unknown format %q (want json or text)", format)
	}
}

func renderText(rep *report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "keystream reuse audit: %d fragment(s), %d key/nonce group(s)\n", rep.FragmentCount, rep.GroupCount)
	for _, g := range rep.Groups {
		fmt.Fprintf(&b, "\n== key_id=%s nonce=%s ==\n", g.KeyID, g.Nonce)
		fmt.Fprintf(&b, "recovered=%d conflict_positions=%d\n", g.RecoveredByteCount, g.ConflictCount)
		if len(g.Overlaps) == 0 {
			b.WriteString("overlaps: none\n")
		} else {
			b.WriteString("overlaps:\n")
			for _, ov := range g.Overlaps {
				fmt.Fprintf(&b, "  %s<->%s blocks [%d..%d] positions [%d..%d) bytes=%d\n",
					ov.FragmentA, ov.FragmentB, ov.StartBlock, ov.EndBlock,
					ov.StartPosition, ov.EndPosition, ov.OverlapBytes)
			}
		}
		b.WriteString("keystream:\n")
		for _, ks := range g.Keystream {
			flag := "ok"
			if ks.Conflict {
				flag = "CONFLICT"
			}
			fmt.Fprintf(&b, "  pos=%d block=%d off=%d %s", ks.Position, ks.Block, ks.OffsetInBlock, flag)
			for _, c := range ks.Candidates {
				fmt.Fprintf(&b, " %s<=from %s#%d", c.KeystreamByte, c.Evidence.FragmentID, c.Evidence.Offset)
			}
			b.WriteByte('\n')
		}
		b.WriteString("fragments:\n")
		for _, fr := range g.Fragments {
			fmt.Fprintf(&b, "  %s positions [%d..%d) known=%d recovered=%d conflict=%d\n",
				fr.FragmentID, fr.AbsStart, fr.AbsEnd, fr.KnownCount, fr.RecoveredCount, fr.ConflictCount)
			for _, rb := range fr.Recovered {
				origin := "propagated"
				if rb.FromKnownPlaintext {
					origin = "known"
				}
				flag := ""
				if rb.Conflict {
					flag = " CONFLICT"
				}
				fmt.Fprintf(&b, "    off=%d pos=%d pt=%s src=%s#%d %s%s\n",
					rb.Offset, rb.Position, rb.PlaintextByte,
					rb.Source.FragmentID, rb.Source.Offset, origin, flag)
			}
		}
	}
	return b.String()
}
