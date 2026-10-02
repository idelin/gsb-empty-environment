package exprfmt

import (
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) Expr {
	t.Helper()
	e, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return e
}

func maxLineLen(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if len(line) > max {
			max = len(line)
		}
	}
	return max
}

// checkRoundTrip formats e, re-parses the output and requires an identical
// AST and lines within MaxWidth.
func checkRoundTrip(t *testing.T, e Expr) string {
	t.Helper()
	out := Format(e)
	again, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parsing formatted output failed: %v\noutput:\n%s", err, out)
	}
	if !reflect.DeepEqual(e, again) {
		t.Fatalf("round-trip AST mismatch\noriginal: %#v\nreparsed: %#v\noutput:\n%s", e, again, out)
	}
	if w := maxLineLen(out); w > MaxWidth {
		t.Fatalf("line width %d exceeds %d\noutput:\n%s", w, MaxWidth, out)
	}
	return out
}

func TestFormatFlat(t *testing.T) {
	e := mustParse(t, "alpha + beta * gamma")
	if got := Format(e); got != "alpha + beta * gamma" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatMinimalParens(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(a + b) * c", "(a + b) * c"},
		{"a * (b + c)", "a * (b + c)"},
		{"a + b * c", "a + b * c"},
		{"a - (b - c)", "a - (b - c)"},
		{"a - b - c", "a - b - c"},
		{"a + (b + c)", "a + (b + c)"},
		{"(a + b) + c", "a + b + c"},
		{"-(a + b)", "-(a + b)"},
		{"!(x == y)", "!(x == y)"},
		{"(a + b)(c)", "(a + b)(c)"},
		{"f(x)(y)", "f(x)(y)"},
	}
	for _, c := range cases {
		if got := Format(mustParse(t, c.in)); got != c.want {
			t.Errorf("Format(Parse(%q)) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatBreaksBinary(t *testing.T) {
	src := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb + cccccccccccccccccccccccccccccc"
	want := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa + bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb +\n" +
		"  cccccccccccccccccccccccccccccc"
	if got := Format(mustParse(t, src)); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatBreaksCall(t *testing.T) {
	src := "computeTotalPrice(basePrice * quantity, discountRate * basePrice * quantity, shippingFee + handlingCharge, loyaltyBonus)"
	want := `computeTotalPrice(
  basePrice * quantity,
  discountRate * basePrice * quantity,
  shippingFee + handlingCharge,
  loyaltyBonus,
)`
	if got := Format(mustParse(t, src)); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRoundTripTable(t *testing.T) {
	cases := []string{
		"1 + 2 * 3",
		"(1 + 2) * 3",
		"a - (b - c)",
		"a + (b + c)",
		"-x * !y",
		"--x",
		`f(g(h(x)), 1, "two")`,
		"f()",
		"a && b || c && !d",
		"x < y == (a > b)",
		"longFunctionName(argumentOne, argumentTwo, argumentThree)",
		"processOrder(validateCustomer(customerName, customerEmail), calculateSubtotal(itemPrice * itemQuantity, discountPercent * itemPrice), finalizePayment(cardNumber, expiryDate))",
	}
	for _, src := range cases {
		checkRoundTrip(t, mustParse(t, src))
	}
}

func TestLineWidthDeepNesting(t *testing.T) {
	src := `processOrder(validateCustomer(customerName, customerEmail, customerAddress), calculateSubtotal(itemPrice * itemQuantity, discountPercent * itemPrice, loyaltyPoints / redemptionRate), applyShipping(destinationCountry, packageWeight, packageDimensions, expressDelivery), finalizePayment(cardNumber, expiryDate, securityCode, billingAddress)) && status == "active" || overrideApproved`
	out := checkRoundTrip(t, mustParse(t, src))
	t.Logf("formatted output:\n%s", out)
}

func TestLineWidthLongChain(t *testing.T) {
	parts := make([]string, 30)
	for i := range parts {
		parts[i] = fmt.Sprintf("operand%02dxxxx", i)
	}
	out := checkRoundTrip(t, mustParse(t, strings.Join(parts, " + ")))
	t.Logf("formatted output:\n%s", out)
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"1 +", "f(", "a b", "@", `"unterminated`, "(a"} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", src)
		}
	}
}

var (
	randBinOps = []string{"||", "&&", "==", "!=", "<", "<=", ">", ">=", "+", "-", "*", "/", "%"}
	randUnOps  = []string{"-", "!"}
)

func randIdent(r *rand.Rand) string {
	n := 1 + r.Intn(20)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(byte('a' + r.Intn(26)))
	}
	return b.String()
}

func genExpr(r *rand.Rand, depth int) Expr {
	if depth == 0 || r.Intn(10) < 3 {
		switch r.Intn(3) {
		case 0:
			return &Ident{Name: randIdent(r)}
		case 1:
			return &Number{Value: strconv.Itoa(r.Intn(1000))}
		default:
			return &Str{Value: strconv.Quote(randIdent(r))}
		}
	}
	switch r.Intn(4) {
	case 0:
		return &Binary{Op: randBinOps[r.Intn(len(randBinOps))], X: genExpr(r, depth-1), Y: genExpr(r, depth-1)}
	case 1:
		return &Unary{Op: randUnOps[r.Intn(len(randUnOps))], X: genExpr(r, depth-1)}
	default:
		var args []Expr
		for i, n := 0, r.Intn(4); i < n; i++ {
			args = append(args, genExpr(r, depth-1))
		}
		return &Call{Func: &Ident{Name: randIdent(r)}, Args: args}
	}
}

func TestRandomRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		e := genExpr(r, 5)
		out := Format(e)
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("case %d: re-parsing failed: %v\noutput:\n%s", i, err, out)
		}
		if !reflect.DeepEqual(e, again) {
			t.Fatalf("case %d: round-trip AST mismatch\noriginal: %#v\nreparsed: %#v\noutput:\n%s", i, e, again, out)
		}
	}
}
