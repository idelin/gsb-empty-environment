package netsec

import (
	"fmt"
	"strings"
)

// FormatPartition renders one common refinement: every crossing equivalence
// class with its members, prior mass and Bayes guess, followed by both
// identification rates.
func (a *Analysis) FormatPartition(partition *Partition) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "plugins=%s classes=%d\n",
		maskName(a, partition.Mask), len(partition.Classes))
	for idx, class := range partition.Classes {
		fmt.Fprintf(&builder, "  class %d: secrets=%s mass=%.6f guess=%s guess_mass=%.6f\n",
			idx, strings.Join(class.Secrets, ","), class.Mass, class.Guess, class.GuessMass)
	}
	fmt.Fprintf(&builder, "  bayes_rate=%.6f singleton_rate=%.6f\n",
		partition.BayesRate, partition.SingletonRate)
	return builder.String()
}

// FormatVerifyReport renders the result of Verify in a human-readable form.
func FormatVerifyReport(report VerifyReport) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "verified subsets=%d over secrets=%d: ",
		report.Subsets, report.Secrets)
	if report.OK() {
		builder.WriteString("all cross-checks agree\n")
		return builder.String()
	}
	fmt.Fprintf(&builder, "%d mismatch(es)\n", len(report.Mismatches))
	for _, mismatch := range report.Mismatches {
		builder.WriteString("  - ")
		builder.WriteString(mismatch)
		builder.WriteByte('\n')
	}
	return builder.String()
}
