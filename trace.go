package netsec

import "strings"

// RenderTrace returns an indented, human-readable rendering of every
// rule evaluation, including tag mutations, match/skip outcomes,
// nested sub-policy calls and the final verdict.
func RenderTrace(trace *Trace) string {
	if trace == nil {
		return ""
	}
	var b strings.Builder
	for _, step := range trace.Steps {
		renderStep(&b, step, 0)
	}
	b.WriteString("=> ")
	b.WriteString(trace.Final.String())
	b.WriteByte('\n')
	return b.String()
}

func renderStep(b *strings.Builder, step *Step, depth int) {
	indent := strings.Repeat("  ", depth)
	switch step.Kind {
	case StepEnterPolicy:
		b.WriteString(indent)
		b.WriteString("enter policy ")
		b.WriteString(step.Policy)
		b.WriteString(" tags=")
		b.WriteString(step.BeforeTags.String())
		b.WriteByte('\n')
		for _, child := range step.Children {
			renderStep(b, child, depth+1)
		}
	case StepLeavePolicy:
		b.WriteString(indent)
		b.WriteString("leave policy ")
		b.WriteString(step.Policy)
		b.WriteString(" verdict=")
		b.WriteString(step.Verdict.String())
		b.WriteByte('\n')
	case StepRuleMatch:
		b.WriteString(indent)
		b.WriteString("rule ")
		b.WriteString(step.Rule)
		b.WriteString(" MATCH ")
		b.WriteString(step.BeforeTags.String())
		b.WriteString(" -> ")
		b.WriteString(step.AfterTags.String())
		if step.Verdict != VerdictNone {
			b.WriteString(" ")
			b.WriteString(step.Verdict.String())
		}
		b.WriteByte('\n')
		for _, child := range step.Children {
			renderStep(b, child, depth+1)
		}
	case StepRuleSkip:
		b.WriteString(indent)
		b.WriteString("rule ")
		b.WriteString(step.Rule)
		b.WriteString(" skip\n")
	case StepEarlySkip:
		b.WriteString(indent)
		b.WriteString("rule ")
		b.WriteString(step.Rule)
		b.WriteString(" not-run (earlier verdict)\n")
	}
}
