package gsb

import (
	"fmt"
	"strings"
)

func materialName(p *Problem, i int) string {
	if p.Materials[i].Name != "" {
		return p.Materials[i].Name
	}
	return fmt.Sprintf("M%d", i)
}

func demandName(p *Problem, i int) string {
	if p.Demands[i].Name != "" {
		return p.Demands[i].Name
	}
	return fmt.Sprintf("D%d", i)
}

// Report renders the solution's consumption evidence as a human-readable
// table: how each demand was fulfilled and how much of every material was
// consumed versus its stock.
func (s *Solution) Report(p *Problem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "complete orders: %d\n", s.CompleteOrders)
	fmt.Fprintf(&b, "total cost: %d\n", s.Cost)
	b.WriteString("consumption evidence:\n")
	for _, ln := range s.Lines {
		opt := p.Demands[ln.Demand].Options[ln.Option]
		parts := []string{
			fmt.Sprintf("%s x%d", materialName(p, opt.Material), opt.Ratio*ln.Units),
		}
		for _, a := range opt.Aux {
			parts = append(parts, fmt.Sprintf("%s x%d", materialName(p, a.Material), a.Amount*ln.Units))
		}
		fmt.Fprintf(&b, "  demand %q: %d unit(s) via option %d [%s]\n",
			demandName(p, ln.Demand), ln.Units, ln.Option, strings.Join(parts, ", "))
	}
	b.WriteString("material usage (used/stock):\n")
	for _, u := range s.Usage {
		if u.Used == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s: %d/%d\n", materialName(p, u.Material), u.Used, u.Stock)
	}
	return b.String()
}
