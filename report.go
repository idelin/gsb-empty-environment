package main

import (
	"fmt"
	"strings"
)

// RenderReport formats every finalized connection epoch, its two
// reassembled directions (with [GAP n] markers), conflicts, and all
// dropped packets.
func RenderReport(o *Observer) string {
	var b strings.Builder
	b.WriteString("=== reconstruction report ===\n")
	for _, c := range o.History() {
		fmt.Fprintf(&b, "\nconnection %s %s epoch %d\n", c.ID, c.Tuple, c.Epoch)
		fmt.Fprintf(&b, "  final state: %s\n", c.stateString())
		for d := C2S; d <= S2C; d++ {
			st := c.Streams[d]
			tail := " [OPEN]"
			switch {
			case st.hasFin:
				tail = " [FIN]"
			case c.rst:
				tail = " [RST]"
			}
			fmt.Fprintf(&b, "  %s: %s%s\n", d, st.Render(), tail)
			if n := len(st.conflicts); n > 0 {
				fmt.Fprintf(&b, "  conflicts on %s: %d (kept first-seen bytes)\n", d, n)
				shown := st.conflicts
				if len(shown) > 5 {
					shown = shown[:5]
				}
				for _, cf := range shown {
					fmt.Fprintf(&b, "    seq=%d kept 0x%02x rejected 0x%02x\n",
						cf.Seq, cf.Existing, cf.Incoming)
				}
				if len(st.conflicts) > 5 {
					fmt.Fprintf(&b, "    ... and %d more\n", len(st.conflicts)-5)
				}
			}
		}
	}
	if len(o.Drops) > 0 {
		b.WriteString("\ndropped packets:\n")
		for _, d := range o.Drops {
			fmt.Fprintf(&b, "  tick %d: %s reason=%s\n", d.Tick, d.Packet.describe(), d.Reason)
		}
	}
	return b.String()
}
