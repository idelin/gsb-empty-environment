package ratbez

import (
	"encoding/json"
	"math/big"
	"testing"
)

func rats(ss ...string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			panic("bad test rational: " + s)
		}
		out[i] = r
	}
	return out
}

func analyzeAndVerify(t *testing.T, P, W []string, eps string) *Cert {
	t.Helper()
	cert, err := Analyze(rats(P...), rats(W...), rats(eps)[0])
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if err := Verify(cert); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := SampleCheck(cert, 1000); err != nil {
		t.Fatalf("SampleCheck: %v", err)
	}
	return cert
}

func groupOf(cert *Cert, g int) []CandCert {
	var out []CandCert
	for _, cd := range cert.Candidates {
		if cd.Group == g {
			out = append(out, cd)
		}
	}
	return out
}

func kinds(members []CandCert) map[string]int {
	out := map[string]int{}
	for _, m := range members {
		out[m.Kind]++
	}
	return out
}

func mustRat(s string) *big.Rat { return rats(s)[0] }

// Degree 1: strictly monotone, no interior stationary points.
func TestLinear(t *testing.T) {
	cert := analyzeAndVerify(t, []string{"0", "2"}, []string{"1", "1"}, "1/100")
	if len(cert.Roots) != 0 {
		t.Fatalf("expected no interior stationary points, got %d", len(cert.Roots))
	}
	if len(cert.Segments) != 1 || cert.Segments[0].Sign != 1 {
		t.Fatalf("expected a single increasing segment, got %+v", cert.Segments)
	}
	max := groupOf(cert, cert.MaxGroup)
	min := groupOf(cert, cert.MinGroup)
	if len(max) != 1 || max[0].Kind != KindT1 || cert.MaxLo.Cmp(mustRat("2")) != 0 || cert.MaxHi.Cmp(mustRat("2")) != 0 {
		t.Fatalf("max should be exactly 2 at t=1: %+v", max)
	}
	if len(min) != 1 || min[0].Kind != KindT0 || cert.MinLo.Cmp(mustRat("0")) != 0 {
		t.Fatalf("min should be exactly 0 at t=0: %+v", min)
	}
}

// Symmetric quadratic R(t) = 2t(1-t): exact rational stationary point t=1/2,
// and the minimum 0 is attained at BOTH endpoints (tie must be preserved).
func TestSymmetricQuadratic(t *testing.T) {
	cert := analyzeAndVerify(t, []string{"0", "1", "0"}, []string{"1", "1", "1"}, "1/1000")
	if len(cert.Roots) != 1 {
		t.Fatalf("expected 1 stationary point, got %d", len(cert.Roots))
	}
	r := cert.Roots[0]
	if r.Lo.Cmp(mustRat("1/2")) != 0 || r.Hi.Cmp(mustRat("1/2")) != 0 {
		t.Fatalf("stationary point should be exactly 1/2, got [%s, %s]", r.Lo, r.Hi)
	}
	if cert.Segments[0].Sign != 1 || cert.Segments[1].Sign != -1 {
		t.Fatalf("expected increasing then decreasing, got %+v", cert.Segments)
	}
	max := groupOf(cert, cert.MaxGroup)
	if len(max) != 1 || max[0].Kind != KindRoot {
		t.Fatalf("max should be the interior stationary point: %+v", max)
	}
	if cert.MaxLo.Cmp(mustRat("1/2")) != 0 || cert.MaxHi.Cmp(mustRat("1/2")) != 0 {
		t.Fatalf("max value should be exactly 1/2, got [%s, %s]", cert.MaxLo, cert.MaxHi)
	}
	min := groupOf(cert, cert.MinGroup)
	if len(min) != 2 || kinds(min)[KindT0] != 1 || kinds(min)[KindT1] != 1 {
		t.Fatalf("min should be attained at BOTH endpoints: %+v", min)
	}
	if cert.MinLo.Cmp(mustRat("0")) != 0 || cert.MinHi.Cmp(mustRat("0")) != 0 {
		t.Fatalf("min value should be exactly 0, got [%s, %s]", cert.MinLo, cert.MinHi)
	}
}

// Constant curve: the whole interval [0,1] attains both extrema.
func TestConstant(t *testing.T) {
	cert := analyzeAndVerify(t, []string{"3/2", "3/2", "3/2"}, []string{"1", "2", "1"}, "1/1000")
	if !cert.Constant {
		t.Fatalf("expected constant curve")
	}
	if cert.ConstValue.Cmp(mustRat("3/2")) != 0 {
		t.Fatalf("constant value should be 3/2, got %s", cert.ConstValue)
	}
}

// Repeated stationary point: R(t) = (t-1/2)^3 has a double root of the
// derivative at t=1/2; the curve is monotone through it (terrace).
func TestDoubleRoot(t *testing.T) {
	cert := analyzeAndVerify(t,
		[]string{"-1/8", "1/8", "-1/8", "1/8"},
		[]string{"1", "1", "1", "1"}, "1/1000")
	if len(cert.Roots) != 1 {
		t.Fatalf("expected 1 (repeated) stationary point, got %d", len(cert.Roots))
	}
	r := cert.Roots[0]
	if r.Lo.Cmp(mustRat("1/2")) != 0 || r.Hi.Cmp(mustRat("1/2")) != 0 {
		t.Fatalf("stationary point should be exactly 1/2, got [%s, %s]", r.Lo, r.Hi)
	}
	if cert.Segments[0].Sign != 1 || cert.Segments[1].Sign != 1 {
		t.Fatalf("expected increasing on both sides (terrace), got %+v", cert.Segments)
	}
	max := groupOf(cert, cert.MaxGroup)
	min := groupOf(cert, cert.MinGroup)
	if len(max) != 1 || max[0].Kind != KindT1 || cert.MaxHi.Cmp(mustRat("1/8")) != 0 {
		t.Fatalf("max should be 1/8 at t=1: %+v", max)
	}
	if len(min) != 1 || min[0].Kind != KindT0 || cert.MinLo.Cmp(mustRat("-1/8")) != 0 {
		t.Fatalf("min should be -1/8 at t=0: %+v", min)
	}
	// The interior stationary point is neither max nor min.
	for _, cd := range cert.Candidates {
		if cd.Kind == KindRoot && (cd.Group == cert.MaxGroup || cd.Group == cert.MinGroup) {
			t.Fatalf("terrace point must not be an extremum")
		}
	}
}

// Two stationary points only 1e-6 apart (a local max at t=1/2 and a local
// min at t=1/2+1e-6); their values differ by only ~1.7e-19. Exact isolation
// must still separate them and get the monotonicity +,-,+ right.
func TestCloseRoots(t *testing.T) {
	// R(t) = integral of (s-1/2)(s-1/2-1e-6) ds, as a cubic Bezier:
	// control values (0, 1/12+1e-6/6, 1e-6/6, 1/12).
	cert := analyzeAndVerify(t,
		[]string{"0", "500001/6000000", "1/6000000", "1/12"},
		[]string{"1", "1", "1", "1"}, "1/1000000000")
	if len(cert.Roots) != 2 {
		t.Fatalf("expected 2 stationary points, got %d", len(cert.Roots))
	}
	if cert.Roots[0].Hi.Cmp(cert.Roots[1].Lo) >= 0 {
		t.Fatalf("isolating intervals must be disjoint: %+v", cert.Roots)
	}
	// First root is exactly 1/2; the second lies in (1/2, 1/2 + 1/1000).
	if cert.Roots[0].Lo.Cmp(mustRat("1/2")) != 0 || cert.Roots[0].Hi.Cmp(mustRat("1/2")) != 0 {
		t.Fatalf("first stationary point should be exactly 1/2, got %+v", cert.Roots[0])
	}
	if cert.Roots[1].Lo.Cmp(mustRat("1/2")) <= 0 || cert.Roots[1].Hi.Cmp(mustRat("501/1000")) >= 0 {
		t.Fatalf("second stationary point should be just above 1/2, got %+v", cert.Roots[1])
	}
	want := []int{1, -1, 1}
	for i, s := range cert.Segments {
		if s.Sign != want[i] {
			t.Fatalf("segment %d: expected sign %d, got %d", i, want[i], s.Sign)
		}
	}
	max := groupOf(cert, cert.MaxGroup)
	min := groupOf(cert, cert.MinGroup)
	if len(max) != 1 || max[0].Kind != KindT1 || cert.MaxHi.Cmp(mustRat("1/12")) != 0 {
		t.Fatalf("max should be 1/12 at t=1: %+v", max)
	}
	if len(min) != 1 || min[0].Kind != KindT0 || cert.MinLo.Cmp(mustRat("0")) != 0 {
		t.Fatalf("min should be 0 at t=0: %+v", min)
	}
	// The two interior stationary values are distinct groups (they differ by
	// ~1.7e-19) and neither is a global extremum.
	var interior []CandCert
	for _, cd := range cert.Candidates {
		if cd.Kind == KindRoot {
			interior = append(interior, cd)
		}
	}
	if len(interior) != 2 || interior[0].Group == interior[1].Group {
		t.Fatalf("the two close stationary values must be separated: %+v", interior)
	}
}

// R(t) = -((t-1/3)(t-2/3))^2: two distinct interior stationary points
// (t=1/3 and t=2/3) attain the SAME global maximum 0. The tie must be
// detected exactly (via the value polynomial) and both parameters kept.
func TestTwoEqualMaxima(t *testing.T) {
	cert := analyzeAndVerify(t,
		[]string{"-4/81", "5/81", "-11/162", "5/81", "-4/81"},
		[]string{"1", "1", "1", "1", "1"}, "1/10000")
	if len(cert.Roots) != 3 {
		t.Fatalf("expected 3 stationary points, got %d", len(cert.Roots))
	}
	want := []int{1, -1, 1, -1}
	for i, s := range cert.Segments {
		if s.Sign != want[i] {
			t.Fatalf("segment %d: expected sign %d, got %d", i, want[i], s.Sign)
		}
	}
	max := groupOf(cert, cert.MaxGroup)
	if len(max) != 2 || kinds(max)[KindRoot] != 2 {
		t.Fatalf("max should be attained at two interior points: %+v", max)
	}
	zero := mustRat("0")
	if cert.MaxLo.Cmp(zero) > 0 || cert.MaxHi.Cmp(zero) < 0 {
		t.Fatalf("max enclosure [%s, %s] must contain 0", cert.MaxLo, cert.MaxHi)
	}
	if new(big.Rat).Sub(cert.MaxHi, cert.MaxLo).Cmp(mustRat("1/10000")) > 0 {
		t.Fatalf("max enclosure wider than eps")
	}
	// The two maxima sit near 1/3 and 2/3.
	lo1, hi1 := max[0].TL, max[0].TH
	lo2, hi2 := max[1].TL, max[1].TH
	if lo1.Cmp(mustRat("1/3")) > 0 || hi1.Cmp(mustRat("1/3")) < 0 ||
		lo2.Cmp(mustRat("2/3")) > 0 || hi2.Cmp(mustRat("2/3")) < 0 {
		t.Fatalf("maxima should enclose 1/3 and 2/3: %s %s", paramDesc(max[0]), paramDesc(max[1]))
	}
	// Global min: both endpoints tie at -4/81.
	min := groupOf(cert, cert.MinGroup)
	if len(min) != 2 || kinds(min)[KindT0] != 1 || kinds(min)[KindT1] != 1 {
		t.Fatalf("min should be attained at both endpoints: %+v", min)
	}
	if cert.MinLo.Cmp(mustRat("-4/81")) != 0 {
		t.Fatalf("min value should be exactly -4/81, got %s", cert.MinLo)
	}
	// The middle stationary point (t=1/2) is a local min with value -1/1296.
	for _, cd := range cert.Candidates {
		if cd.Kind == KindRoot && cd.Group != cert.MaxGroup {
			v := mustRat("-1/1296")
			if cd.VLo.Cmp(v) > 0 || cd.VHi.Cmp(v) < 0 {
				t.Fatalf("local min enclosure [%s, %s] must contain -1/1296", cd.VLo, cd.VHi)
			}
		}
	}
}

// Non-uniform positive rational weights: R(1/2) = 2/3 exactly.
func TestRationalWeights(t *testing.T) {
	cert := analyzeAndVerify(t, []string{"0", "1", "0"}, []string{"1", "2", "1"}, "1/1000")
	if len(cert.Roots) != 1 {
		t.Fatalf("expected 1 stationary point, got %d", len(cert.Roots))
	}
	max := groupOf(cert, cert.MaxGroup)
	if len(max) != 1 || max[0].Kind != KindRoot {
		t.Fatalf("max should be the interior stationary point: %+v", max)
	}
	v := mustRat("2/3")
	if cert.MaxLo.Cmp(v) > 0 || cert.MaxHi.Cmp(v) < 0 {
		t.Fatalf("max enclosure [%s, %s] must contain 2/3", cert.MaxLo, cert.MaxHi)
	}
	min := groupOf(cert, cert.MinGroup)
	if len(min) != 2 || cert.MinLo.Cmp(mustRat("0")) != 0 {
		t.Fatalf("min should be 0 at both endpoints: %+v", min)
	}
}

// Derivative zero exactly at the endpoint t=0 (R(t) = t^2): the endpoint
// root must not appear as an interior stationary point.
func TestEndpointStationary(t *testing.T) {
	cert := analyzeAndVerify(t, []string{"0", "0", "1"}, []string{"1", "1", "1"}, "1/1000")
	if len(cert.Roots) != 0 {
		t.Fatalf("expected no interior stationary points, got %d", len(cert.Roots))
	}
	if cert.Segments[0].Sign != 1 {
		t.Fatalf("expected increasing")
	}
	if cert.MinLo.Cmp(mustRat("0")) != 0 || cert.MaxHi.Cmp(mustRat("1")) != 0 {
		t.Fatalf("min 0 / max 1 expected")
	}
}

// Degree-5 smoke test with several oscillations.
func TestDegree5(t *testing.T) {
	cert := analyzeAndVerify(t,
		[]string{"0", "1", "0", "1", "0", "1"},
		[]string{"1", "1", "1", "1", "1", "1"}, "1/1000")
	if len(cert.Roots) == 0 {
		t.Fatalf("expected interior stationary points")
	}
	t.Logf("\n%s", Report(cert))
}

// Input validation.
func TestValidation(t *testing.T) {
	if _, err := Analyze(rats("0", "1"), rats("1", "-1"), mustRat("1/10")); err == nil {
		t.Fatalf("negative weight must be rejected")
	}
	if _, err := Analyze(rats("0", "1"), rats("1", "1"), mustRat("0")); err == nil {
		t.Fatalf("non-positive eps must be rejected")
	}
	if _, err := Analyze(rats("0", "1"), rats("1"), mustRat("1/10")); err == nil {
		t.Fatalf("length mismatch must be rejected")
	}
}

func cloneCert(t *testing.T, c *Cert) *Cert {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var cc Cert
	if err := json.Unmarshal(data, &cc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &cc
}

// The checker must reject tampered certificates.
func TestTamperedCertificates(t *testing.T) {
	base := analyzeAndVerify(t, []string{"0", "1", "0"}, []string{"1", "1", "1"}, "1/1000")

	cases := map[string]func(*Cert){
		"flip monotonicity sign": func(c *Cert) { c.Segments[0].Sign = -c.Segments[0].Sign },
		"drop a root":            func(c *Cert) { c.Roots = c.Roots[:0] },
		"swap max/min groups":    func(c *Cert) { c.MaxGroup, c.MinGroup = c.MinGroup, c.MaxGroup },
		"move a root interval": func(c *Cert) {
			c.Roots[0].Lo = mustRat("1/3")
			c.Roots[0].Hi = mustRat("1/3")
		},
		"wrong max enclosure":    func(c *Cert) { c.MaxLo = mustRat("1") },
		"wrong group assignment": func(c *Cert) { c.Candidates[0].Group = 999 },
		"wrong M":                func(c *Cert) { c.MPrim[0] = "12345" },
		"enclosure too wide":     func(c *Cert) { c.MaxHi = new(big.Rat).Add(c.MaxHi, mustRat("1/10")) },
	}
	for name, tamper := range cases {
		c := cloneCert(t, base)
		tamper(c)
		if err := Verify(c); err == nil {
			t.Fatalf("tampered certificate (%s) was accepted", name)
		}
	}
}

// Certificates survive a JSON round trip and still verify.
func TestJSONRoundTrip(t *testing.T) {
	base := analyzeAndVerify(t,
		[]string{"-4/81", "5/81", "-11/162", "5/81", "-4/81"},
		[]string{"1", "1", "1", "1", "1"}, "1/10000")
	c := cloneCert(t, base)
	if err := Verify(c); err != nil {
		t.Fatalf("Verify after JSON round trip: %v", err)
	}
}
