package updatestrategy

import (
	"fmt"
	"strings"
)

// Dump renders the policy as an observation-branching tree. Each non-goal
// node names the single action the host takes from that belief and lists the
// successor chosen for every public observation that can follow.
func (p *Policy) Dump() string {
	var b strings.Builder
	dumpNode(&b, p.Root, "")
	return b.String()
}

func dumpNode(b *strings.Builder, n *PolicyNode, indent string) {
	if n == nil {
		fmt.Fprintf(b, "%s<nil>\n", indent)
		return
	}
	if n.IsGoal {
		fmt.Fprintf(b, "%sGOAL (size=%d)\n", indent, n.Belief.Size())
		return
	}
	fmt.Fprintf(b, "%s%s [worst=%d, belief=%d worlds]\n", indent, n.Action.Name(), n.WorstCost, n.Belief.Size())
	next := indent + "    "
	for obs, child := range n.Branches {
		fmt.Fprintf(b, "%son %s ->\n", next, describeObs(obs))
		dumpNode(b, child, next+"  ")
	}
}

func describeObs(o Observation) string {
	switch o.Kind {
	case ObsReboot, ObsInitial:
		return fmt.Sprintf("%s(active=%s,digest=%s,counter=%d)", o.Kind, o.ActiveSlot, o.ActiveDigest, o.Counter)
	case ObsWriteAck, ObsWriteNoAck:
		return fmt.Sprintf("%s(ack=%t,counter=%d)", o.Kind, o.Ack, o.Counter)
	default:
		return fmt.Sprintf("%s(counter=%d)", o.Kind, o.Counter)
	}
}
