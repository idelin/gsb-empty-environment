package symbolbind

import (
	"fmt"
	"strings"
)

func (trace *Trace) String() string {
	if trace == nil {
		return "<nil trace>"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "relocation: origin=%s symbol=%s@%s interposition=%t require-trusted=%t\n",
		trace.Relocation.OriginID,
		trace.Relocation.Name,
		trace.Relocation.Version,
		trace.Relocation.AllowInterposition,
		trace.Relocation.RequireTrusted,
	)

	builder.WriteString("sequence:\n")
	for index, entry := range trace.Sequence {
		fmt.Fprintf(&builder, "  %d. %s (%s)\n", index+1, entry.ModuleID, entry.Source)
	}

	builder.WriteString("inspections:\n")
	for _, inspection := range trace.Inspections {
		if inspection.Matched {
			fmt.Fprintf(&builder, "  %d. %s matched %s exporter=%s definer=%s\n",
				inspection.Step,
				inspection.ModuleID,
				inspection.Strength,
				inspection.ExporterID,
				inspection.DefinerID,
			)
			continue
		}
		fmt.Fprintf(&builder, "  %d. %s skipped: %s\n", inspection.Step, inspection.ModuleID, inspection.SkipReason)
	}

	builder.WriteString("candidates:\n")
	if len(trace.Candidates) == 0 {
		builder.WriteString("  none\n")
	}
	for _, candidate := range trace.Candidates {
		fmt.Fprintf(&builder, "  %d. exporter=%s definer=%s trusted=%t strength=%s\n",
			candidate.Step,
			candidate.ExporterID,
			candidate.DefinerID,
			candidate.DefinerTrusted,
			candidate.Strength,
		)
	}

	if trace.Winner == nil {
		builder.WriteString("winner: unresolved\n")
	} else {
		fmt.Fprintf(&builder, "winner: exporter=%s definer=%s trusted=%t strength=%s\n",
			trace.Winner.ExporterID,
			trace.Winner.DefinerID,
			trace.Winner.DefinerTrusted,
			trace.Winner.Strength,
		)
	}
	fmt.Fprintf(&builder, "reason: %s\n", trace.Reason)
	return builder.String()
}

func (result *AttackResult) String() string {
	if result == nil {
		return "<nil attack result>"
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "status=%s plugin-count=%d scheme-limit=%d truncated=%t\n",
		result.Status, result.PluginCount, result.Limit, result.Truncated)
	fmt.Fprintf(&builder, "reason: %s\n", result.Reason)
	for index, scheme := range result.Schemes {
		fmt.Fprintf(&builder, "scheme %d: global=%s insertions=", index+1, strings.Join(scheme.GlobalOrder, ","))
		for insertionIndex, insertion := range scheme.Insertions {
			if insertionIndex > 0 {
				builder.WriteString(",")
			}
			fmt.Fprintf(&builder, "%s@%d", insertion.PluginID, insertion.Position)
		}
		builder.WriteString("\n")
		if scheme.Trace != nil {
			builder.WriteString(scheme.Trace.String())
		}
	}
	return builder.String()
}
