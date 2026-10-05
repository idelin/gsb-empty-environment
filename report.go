package ratbez

import (
	"fmt"
	"math/big"
	"strings"
)

func mono(sign int) string {
	if sign > 0 {
		return "increasing"
	}
	return "decreasing"
}

func classify(left, right int) string {
	switch {
	case left > 0 && right < 0:
		return "local maximum"
	case left < 0 && right > 0:
		return "local minimum"
	case left > 0 && right > 0:
		return "stationary terrace (increasing on both sides)"
	default:
		return "stationary terrace (decreasing on both sides)"
	}
}

func paramDesc(cd CandCert) string {
	switch cd.Kind {
	case KindT0:
		return "t = 0 (endpoint)"
	case KindT1:
		return "t = 1 (endpoint)"
	default:
		if cd.TL.Cmp(cd.TH) == 0 {
			return fmt.Sprintf("t = %s (exact rational stationary point)", cd.TL.RatString())
		}
		return fmt.Sprintf("t in [%s, %s] (stationary point, width %s)",
			cd.TL.RatString(), cd.TH.RatString(), new(big.Rat).Sub(cd.TH, cd.TL).RatString())
	}
}

// Report renders a human-readable summary of a (verified) certificate.
func Report(c *Cert) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rational Bezier curve: degree %d (%d control values), positive rational weights\n",
		len(c.P)-1, len(c.P))
	fmt.Fprintf(&b, "Required enclosure width eps = %s\n", c.Eps.RatString())
	if c.Constant {
		fmt.Fprintf(&b, "The curve is constant: R(t) = %s for all t in [0,1].\n", c.ConstValue.RatString())
		fmt.Fprintf(&b, "Global max = global min = %s, attained on the whole interval [0,1].\n",
			c.ConstValue.RatString())
		return b.String()
	}
	b.WriteString("Denominator strictly positive on [0,1]: certified (Sturm)\n")
	fmt.Fprintf(&b, "Interior stationary points: %d\n", len(c.Roots))
	for i, r := range c.Roots {
		l, rt := c.Segments[i].Sign, c.Segments[i+1].Sign
		if r.Lo.Cmp(r.Hi) == 0 {
			fmt.Fprintf(&b, "  #%d: t = %s (exact)\n", i+1, r.Lo.RatString())
		} else {
			fmt.Fprintf(&b, "  #%d: t in [%s, %s]\n", i+1, r.Lo.RatString(), r.Hi.RatString())
		}
		fmt.Fprintf(&b, "      left: %s, right: %s  ->  %s\n", mono(l), mono(rt), classify(l, rt))
	}
	for _, kind := range []string{KindT0, KindT1} {
		for _, cd := range c.Candidates {
			if cd.Kind == kind {
				fmt.Fprintf(&b, "Endpoint %s: value %s\n", paramDesc(cd), cd.VLo.RatString())
			}
		}
	}
	width := new(big.Rat).Sub(c.MaxHi, c.MaxLo)
	fmt.Fprintf(&b, "Global max: value in [%s, %s]  (width %s <= eps)\n",
		c.MaxLo.RatString(), c.MaxHi.RatString(), width.RatString())
	for _, cd := range c.Candidates {
		if cd.Group == c.MaxGroup {
			fmt.Fprintf(&b, "  attained at %s\n", paramDesc(cd))
		}
	}
	width = new(big.Rat).Sub(c.MinHi, c.MinLo)
	fmt.Fprintf(&b, "Global min: value in [%s, %s]  (width %s <= eps)\n",
		c.MinLo.RatString(), c.MinHi.RatString(), width.RatString())
	for _, cd := range c.Candidates {
		if cd.Group == c.MinGroup {
			fmt.Fprintf(&b, "  attained at %s\n", paramDesc(cd))
		}
	}
	return b.String()
}
