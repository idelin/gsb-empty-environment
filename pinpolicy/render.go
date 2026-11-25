package pinpolicy

import (
	"fmt"
	"strings"
)

// RenderText produces a deterministic, line-oriented rendering of a report so
// every partition element, online case, release verdict and the earliest
// failure can be checked independently.
func RenderText(report *Report) string {
	var builder strings.Builder

	builder.WriteString("pinpolicy acceptance report\n")
	builder.WriteString(fmt.Sprintf("pass=%t safety=%t liveness=%t\n",
		report.Pass, report.SafetyPass, report.LivenessPass))
	builder.WriteString(fmt.Sprintf(
		"stats elements=%d empty_intervals=%d online_cases=%d legal_windows=%d releases=%d handovers=%d checkpoints=%d skews=%d blocked_releases=%d\n",
		report.Stats.ElementCount,
		report.Stats.EmptyIntervalCount,
		report.Stats.OnlineCaseCount,
		report.Stats.LegalWindowCount,
		report.Stats.ReleaseCount,
		report.Stats.HandoverCount,
		report.Stats.CheckpointCount,
		report.Stats.SkewCount,
		report.Stats.BlockedReleaseCount,
	))

	for _, window := range report.Windows {
		renderElement(&builder, window)
	}

	if report.EarliestFailure != nil {
		builder.WriteString("earliest_failure " + renderFailure(report.EarliestFailure) + "\n")
	}
	return builder.String()
}

func renderElement(builder *strings.Builder, window OnlineCase) {
	element := window.Element
	switch element.Kind {
	case KindPoint:
		builder.WriteString(fmt.Sprintf("element point t=%d\n", element.At))
	case KindOpen:
		if element.Empty {
			builder.WriteString(fmt.Sprintf(
				"element open (%d,%d) empty=true note=no integer online time inside interval\n",
				element.From, element.Until))
			return
		}
		builder.WriteString(fmt.Sprintf(
			"element open (%d,%d) empty=false sample=%d\n",
			element.From, element.Until, element.Sample))
	}

	builder.WriteString(fmt.Sprintf(
		"  reachable=%t migration_published=%t chain_ok=%t cases=%d\n",
		window.Reachable, window.MigrationFetchable, window.ChainOK, len(window.Cases)))

	for _, key := range window.Cases {
		reason, failed := window.ChainFailure[key]
		status := "chain=ok"
		if failed {
			status = "chain=blocked reason=" + string(reason)
		}
		builder.WriteString(fmt.Sprintf("  case checkpoint=%d skew=%d local=%d %s\n",
			key.Checkpoint, key.Skew, window.RealTime+key.Skew, status))
	}

	for _, verdict := range window.ReleaseVerdicts {
		if len(verdict.Reasons) == 0 {
			builder.WriteString(fmt.Sprintf("  release host=%s seq=%d accept=true\n",
				verdict.Host, verdict.Sequence))
			continue
		}
		builder.WriteString(fmt.Sprintf("  release host=%s seq=%d accept=false reasons=%s\n",
			verdict.Host, verdict.Sequence, joinReasons(verdict.Reasons)))
		for _, missing := range verdict.Missing {
			builder.WriteString(fmt.Sprintf(
				"    missing_handover host=%s digest=%s role=%s side=%s detail=%s\n",
				missing.Host, missing.PolicyDigest, missing.Role,
				missing.MissingSide, missing.Detail))
		}
	}
}

func joinReasons(reasons []Reason) string {
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, string(reason))
	}
	return strings.Join(parts, ",")
}

func renderFailure(failure *Failure) string {
	location := "unknown"
	switch failure.Element.Kind {
	case KindPoint:
		location = fmt.Sprintf("point(t=%d)", failure.Element.At)
	case KindOpen:
		location = fmt.Sprintf("open(%d,%d,sample=%d)",
			failure.Element.From, failure.Element.Until, failure.Element.Sample)
	}
	return fmt.Sprintf(
		"element=%s checkpoint=%d skew=%d host=%s seq=%d reason=%s desc=%q",
		location, failure.Checkpoint, failure.Skew,
		failure.Host, failure.Sequence, failure.Reason, failure.Description)
}
