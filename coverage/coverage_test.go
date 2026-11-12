package coverage

import (
	"errors"
	"math/big"
	"math/rand"
	"testing"
)

func rfrac(num, den int64) *big.Rat { return big.NewRat(num, den) }

func rpt(un, ud, vn, vd int64) RatPoint {
	return RatPoint{U: rfrac(un, ud), V: rfrac(vn, vd)}
}

func ipt(u, v int64) RatPoint {
	// 每次返回独立指针，模拟管线中各顶点独立的 UV 数据。
	return RatPoint{U: big.NewRat(u, 1), V: big.NewRat(v, 1)}
}

func cellKey(c PixelCoverage) [2]int { return [2]int{c.CellX, c.CellY} }

func coverageMap(t *testing.T, cov []PixelCoverage) map[[2]int]PixelCoverage {
	t.Helper()
	m := make(map[[2]int]PixelCoverage, len(cov))
	for _, c := range cov {
		k := cellKey(c)
		if _, dup := m[k]; dup {
			t.Fatalf("duplicate coverage for cell %v", k)
		}
		m[k] = c
	}
	return m
}

func ratEqual(t *testing.T, name string, got, want *big.Rat) {
	t.Helper()
	if got.Cmp(want) != 0 {
		t.Fatalf("%s = %s, want %s", name, got.RatString(), want.RatString())
	}
}

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad test rational: " + s)
	}
	return r
}

// TestDeterministicTriangle 用手算可核对的大三角形验证逐格精确面积、
// 重心坐标和面积矩守恒。
func TestDeterministicTriangle(t *testing.T) {
	tri := Triangle{
		A:           ipt(0, 0),
		B:           ipt(2, 0),
		C:           ipt(0, 2),
		PrimitiveID: 42,
	}
	cov, err := Rasterize(tri)
	if err != nil {
		t.Fatal(err)
	}
	m := coverageMap(t, cov)

	wantArea := map[[2]int]string{
		{0, 0}: "1",
		{1, 0}: "1/2",
		{0, 1}: "1/2",
	}
	if len(m) != len(wantArea) {
		t.Fatalf("got %d cells, want %d: %v", len(m), len(wantArea), m)
	}

	triArea := rfrac(2, 1)
	totalArea := new(big.Rat)
	sumU := new(big.Rat)
	sumV := new(big.Rat)
	for k, want := range wantArea {
		c, ok := m[k]
		if !ok {
			t.Fatalf("missing coverage for cell %v", k)
		}
		ratEqual(t, "area", c.Area, mustRat(want))
		if c.PrimitiveID != 42 {
			t.Fatalf("primitive id = %d, want 42", c.PrimitiveID)
		}
		barySum := new(big.Rat).Add(new(big.Rat).Add(c.Bary[0], c.Bary[1]), c.Bary[2])
		ratEqual(t, "bary sum", barySum, rfrac(1, 1))

		// 质心必须恰好等于重心坐标对三顶点的加权重构。
		// 所有运算都分配新的 Rat，避免原地改写 tri 顶点指针。
		term := func(w, coord *big.Rat) *big.Rat {
			return new(big.Rat).Mul(new(big.Rat).Set(w), new(big.Rat).Set(coord))
		}
		gotU := new(big.Rat).Add(
			new(big.Rat).Add(term(c.Bary[0], tri.A.U), term(c.Bary[1], tri.B.U)),
			term(c.Bary[2], tri.C.U))
		gotV := new(big.Rat).Add(
			new(big.Rat).Add(term(c.Bary[0], tri.A.V), term(c.Bary[1], tri.B.V)),
			term(c.Bary[2], tri.C.V))
		ratEqual(t, "centroid u reconstruct", gotU, c.Centroid.U)
		ratEqual(t, "centroid v reconstruct", gotV, c.Centroid.V)

		totalArea.Add(totalArea, c.Area)
		sumU.Add(sumU, new(big.Rat).Mul(c.Area, c.Centroid.U))
		sumV.Add(sumV, new(big.Rat).Mul(c.Area, c.Centroid.V))

		if f := c.CoverageFloat64(); f <= 0 || f > 1 {
			t.Fatalf("float coverage out of range: %v", f)
		}
		if a, b, g := c.BaryFloat64(); a+b+g < 0.99 || a+b+g > 1.01 {
			t.Fatalf("float bary sum drifted: %v", a+b+g)
		}
	}
	ratEqual(t, "total area", totalArea, triArea)

	// 面积矩守恒：Σ area_i * centroid_i == area_tri * centroid_tri。
	centroidTriU := new(big.Rat).Quo(
		new(big.Rat).Add(new(big.Rat).Add(tri.A.U, tri.B.U), tri.C.U), rfrac(3, 1))
	centroidTriV := new(big.Rat).Quo(
		new(big.Rat).Add(new(big.Rat).Add(tri.A.V, tri.B.V), tri.C.V), rfrac(3, 1))
	ratEqual(t, "moment u", sumU, new(big.Rat).Mul(triArea, centroidTriU))
	ratEqual(t, "moment v", sumV, new(big.Rat).Mul(triArea, centroidTriV))
}

// TestTinyTriangleConservative 证明中心采样会漏掉的细小面，
// 保守覆盖仍能精确捕获。
func TestTinyTriangleConservative(t *testing.T) {
	tri := Triangle{
		A: rpt(1, 4, 1, 4),
		B: rpt(1, 2, 1, 4),
		C: rpt(1, 4, 1, 2),
	}
	cov, err := Rasterize(tri)
	if err != nil {
		t.Fatal(err)
	}
	if len(cov) != 1 {
		t.Fatalf("tiny triangle should touch exactly one cell, got %d", len(cov))
	}
	c := cov[0]
	if c.CellX != 0 || c.CellY != 0 {
		t.Fatalf("unexpected cell (%d,%d)", c.CellX, c.CellY)
	}
	ratEqual(t, "tiny area", c.Area, rfrac(1, 32))

	// 像素中心 (1/2,1/2) 在三角形外：中心采样会得到零覆盖。
	center := ipt(1, 2)
	centerInside := pointInTriangle(tri.A, tri.B, tri.C, center)
	if centerInside {
		t.Fatal("test premise broken: pixel center should be outside")
	}
}

func pointInTriangle(a, b, c, p RatPoint) bool {
	d1 := cross2(a, b, p).Sign()
	d2 := cross2(b, c, p).Sign()
	d3 := cross2(c, a, p).Sign()
	hasNeg := d1 < 0 || d2 < 0 || d3 < 0
	hasPos := d1 > 0 || d2 > 0 || d3 > 0
	return !(hasNeg && hasPos)
}

// TestDegenerateZeroContribution 覆盖共线、重复顶点等退化面。
func TestDegenerateZeroContribution(t *testing.T) {
	cases := []Triangle{
		{A: ipt(0, 0), B: ipt(1, 1), C: ipt(2, 2)},
		{A: ipt(3, 3), B: ipt(3, 3), C: ipt(5, 6)},
		{A: rpt(1, 3, 1, 3), B: rpt(2, 3, 2, 3), C: rpt(5, 6, 5, 6)},
	}
	for i, tri := range cases {
		cov, err := Rasterize(tri)
		if !errors.Is(err, ErrDegenerate) {
			t.Fatalf("case %d: err = %v, want ErrDegenerate", i, err)
		}
		if cov != nil {
			t.Fatalf("case %d: degenerate triangle produced %d cells", i, len(cov))
		}
		cov, err = RasterizeBlock(tri, Rect{MinX: -10, MaxX: 10, MinY: -10, MaxY: 10})
		if !errors.Is(err, ErrDegenerate) || cov != nil {
			t.Fatalf("case %d block: cov=%v err=%v", i, cov, err)
		}
	}
}

// TestSharedEdgeNoDoubleContribution 共享斜边的两个三角形总面积守恒，
// 共享边本身不产生面积贡献。
func TestSharedEdgeNoDoubleContribution(t *testing.T) {
	lower := Triangle{A: ipt(0, 0), B: ipt(1, 0), C: ipt(0, 1), PrimitiveID: 1}
	upper := Triangle{A: ipt(1, 0), B: ipt(0, 1), C: ipt(1, 1), PrimitiveID: 2}

	c1, err := Rasterize(lower)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Rasterize(upper)
	if err != nil {
		t.Fatal(err)
	}
	if len(c1) != 1 || len(c2) != 1 {
		t.Fatalf("each half must only cover cell (0,0): %d %d", len(c1), len(c2))
	}
	total := new(big.Rat).Add(c1[0].Area, c2[0].Area)
	ratEqual(t, "shared-edge total", total, rfrac(1, 1))
	if c1[0].Area.Cmp(rfrac(1, 1)) > 0 || c2[0].Area.Cmp(rfrac(1, 1)) > 0 {
		t.Fatal("a single primitive must not double-count the shared edge")
	}
}

// TestOverlappingUVKeptSeparate 重叠 UV 上不同图元各自保留，不做平均。
func TestOverlappingUVKeptSeparate(t *testing.T) {
	t1 := Triangle{A: ipt(0, 0), B: ipt(1, 0), C: ipt(0, 1), PrimitiveID: 7}
	t2 := Triangle{A: ipt(0, 0), B: ipt(1, 0), C: ipt(1, 1), PrimitiveID: 9}
	c1, err := Rasterize(t1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Rasterize(t2)
	if err != nil {
		t.Fatal(err)
	}
	if len(c1) != 1 || len(c2) != 1 ||
		c1[0].CellX != c2[0].CellX || c1[0].CellY != c2[0].CellY {
		t.Fatalf("overlapping primitives should hit the same cell separately: %v %v", c1, c2)
	}
	if c1[0].PrimitiveID == c2[0].PrimitiveID {
		t.Fatal("primitive identities must be preserved")
	}
	ratEqual(t, "t1 area", c1[0].Area, rfrac(1, 2))
	ratEqual(t, "t2 area", c2[0].Area, rfrac(1, 2))
}

// TestGridAlignedVertex 顶点压在网格线上时不产生多余的零面积方格：
// 大三角形只覆盖三个有面积的方格，小三角形整体落在单个方格内。
func TestGridAlignedVertex(t *testing.T) {
	tri := Triangle{A: ipt(1, 1), B: ipt(3, 1), C: ipt(1, 3)}
	cov, err := Rasterize(tri)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[2]int]string{
		{1, 1}: "1",
		{2, 1}: "1/2",
		{1, 2}: "1/2",
	}
	got := coverageMap(t, cov)
	if len(got) != len(want) {
		t.Fatalf("cells = %v, want %v", got, want)
	}
	for k, area := range want {
		c, ok := got[k]
		if !ok {
			t.Fatalf("missing cell %v", k)
		}
		ratEqual(t, "grid-aligned area", c.Area, mustRat(area))
	}

	single := Triangle{A: ipt(1, 1), B: ipt(2, 1), C: ipt(1, 2)}
	cov, err = Rasterize(single)
	if err != nil {
		t.Fatal(err)
	}
	if len(cov) != 1 || cov[0].CellX != 1 || cov[0].CellY != 1 {
		t.Fatalf("single-cell triangle result = %v", cov)
	}
	ratEqual(t, "single-cell area", cov[0].Area, rfrac(1, 2))
}

// TestOrientationIndependent 顺/逆时针输入结果完全一致。
func TestOrientationIndependent(t *testing.T) {
	cw := Triangle{A: ipt(0, 0), B: ipt(0, 2), C: ipt(2, 0)}
	ccw := Triangle{A: ipt(0, 0), B: ipt(2, 0), C: ipt(0, 2)}
	a, err := Rasterize(cw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Rasterize(ccw)
	if err != nil {
		t.Fatal(err)
	}
	ma, mb := coverageMap(t, a), coverageMap(t, b)
	if len(ma) != len(mb) {
		t.Fatalf("cell count mismatch %d vs %d", len(ma), len(mb))
	}
	for k, va := range ma {
		vb, ok := mb[k]
		if !ok || va.Area.Cmp(vb.Area) != 0 ||
			va.Centroid.U.Cmp(vb.Centroid.U) != 0 ||
			va.Centroid.V.Cmp(vb.Centroid.V) != 0 {
			t.Fatalf("orientation changed result at %v", k)
		}
	}
}

// TestInvalidInputs 覆盖空分块与 nil 分量的防御路径。
func TestInvalidInputs(t *testing.T) {
	if _, err := RasterizeBlock(Triangle{A: ipt(0, 0), B: ipt(1, 0), C: ipt(0, 1)},
		Rect{MinX: 2, MaxX: 1, MinY: 0, MaxY: 1}); err == nil {
		t.Fatal("empty rect should error")
	}
	bad := Triangle{A: RatPoint{U: rfrac(1, 1)}, B: ipt(1, 0), C: ipt(0, 1)}
	if _, err := Rasterize(bad); err == nil {
		t.Fatal("nil uv component should error")
	}
}

// randomSmallTriangle 生成带分数 UV 的随机小三角形，跨度不超过几个像素，
// 专门覆盖细小面与亚像素几何。
func randomSmallTriangle(rng *rand.Rand) Triangle {
	ox := rng.Intn(9) - 4
	oy := rng.Intn(9) - 4
	den := int64(2 + rng.Intn(11)) // 分母 2..12

	mk := func() RatPoint {
		dx := rng.Intn(4) // 0..3 个像素的小跨度
		dy := rng.Intn(4)
		return RatPoint{
			U: big.NewRat(int64(ox)*den+int64(dx), den),
			V: big.NewRat(int64(oy)*den+int64(dy), den),
		}
	}
	return Triangle{A: mk(), B: mk(), C: mk(), PrimitiveID: uint64(rng.Int63())}
}

func assertInvariants(t *testing.T, tri Triangle, cov []PixelCoverage, doubleArea *big.Rat) {
	t.Helper()

	triArea := new(big.Rat).Quo(doubleArea, rfrac(2, 1))
	totalArea := new(big.Rat)
	sumU := new(big.Rat)
	sumV := new(big.Rat)

	seen := make(map[[2]int]bool)
	for _, c := range cov {
		k := cellKey(c)
		if seen[k] {
			t.Fatalf("duplicate cell %v", k)
		}
		seen[k] = true

		if c.Area.Sign() <= 0 || c.Area.Cmp(rfrac(1, 1)) > 0 {
			t.Fatalf("coverage %s out of (0,1] at %v", c.Area.RatString(), k)
		}
		barySum := new(big.Rat).Add(new(big.Rat).Add(c.Bary[0], c.Bary[1]), c.Bary[2])
		if barySum.Cmp(rfrac(1, 1)) != 0 {
			t.Fatalf("bary sum %s != 1 at %v", barySum.RatString(), k)
		}

		// 质心必须落在裁剪多边形所在的单位方格闭包内。
		loX, hiX := big.NewRat(int64(c.CellX), 1), big.NewRat(int64(c.CellX+1), 1)
		loY, hiY := big.NewRat(int64(c.CellY), 1), big.NewRat(int64(c.CellY+1), 1)
		if c.Centroid.U.Cmp(loX) < 0 || c.Centroid.U.Cmp(hiX) > 0 ||
			c.Centroid.V.Cmp(loY) < 0 || c.Centroid.V.Cmp(hiY) > 0 {
			t.Fatalf("centroid %s,%s outside cell %v",
				c.Centroid.U.RatString(), c.Centroid.V.RatString(), k)
		}

		totalArea.Add(totalArea, c.Area)
		sumU.Add(sumU, new(big.Rat).Mul(c.Area, c.Centroid.U))
		sumV.Add(sumV, new(big.Rat).Mul(c.Area, c.Centroid.V))
	}

	if totalArea.Cmp(triArea) != 0 {
		t.Fatalf("area not conserved: sum=%s triangle=%s",
			totalArea.RatString(), triArea.RatString())
	}

	// 面积矩守恒：Σ area*centroid == triangleArea * triangleCentroid。
	centU := new(big.Rat).Quo(
		new(big.Rat).Add(new(big.Rat).Add(tri.A.U, tri.B.U), tri.C.U), rfrac(3, 1))
	centV := new(big.Rat).Quo(
		new(big.Rat).Add(new(big.Rat).Add(tri.A.V, tri.B.V), tri.C.V), rfrac(3, 1))
	wantU := new(big.Rat).Mul(triArea, centU)
	wantV := new(big.Rat).Mul(triArea, centV)
	if sumU.Cmp(wantU) != 0 || sumV.Cmp(wantV) != 0 {
		t.Fatalf("moment not conserved: got (%s,%s) want (%s,%s)",
			sumU.RatString(), sumV.RatString(), wantU.RatString(), wantV.RatString())
	}
}

// TestRandomSmallTrianglesConservation 随机小三角形上的精确总面积守恒、
// 重心与面积矩守恒，以及退化面零贡献。
func TestRandomSmallTrianglesConservation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	nonDegenerate := 0
	for iter := 0; iter < 400; iter++ {
		tri := randomSmallTriangle(rng)
		doubleArea := cross2(tri.A, tri.B, tri.C)
		cov, err := Rasterize(tri)

		if doubleArea.Sign() == 0 {
			if !errors.Is(err, ErrDegenerate) {
				t.Fatalf("iter %d: degenerate err = %v", iter, err)
			}
			if cov != nil {
				t.Fatalf("iter %d: degenerate produced coverage", iter)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		nonDegenerate++
		assertInvariants(t, tri, cov, new(big.Rat).Abs(doubleArea))
	}
	if nonDegenerate < 100 {
		t.Fatalf("too few non-degenerate samples: %d", nonDegenerate)
	}
}

// TestRandomBlockMatchesWhole 多种分块切分拼接结果必须与整图光栅化逐格相等。
func TestRandomBlockMatchesWhole(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for iter := 0; iter < 100; iter++ {
		tri := randomSmallTriangle(rng)
		if cross2(tri.A, tri.B, tri.C).Sign() == 0 {
			continue
		}
		whole, err := Rasterize(tri)
		if err != nil {
			t.Fatal(err)
		}
		want := coverageMap(t, whole)

		// 用随机 1..3 宽的分块覆盖一个比包围盒更大的区域，
		// 同时验证分块外方格不会凭空产生贡献。
		tileW := 1 + rng.Intn(3)
		tileH := 1 + rng.Intn(3)
		got := make(map[[2]int]PixelCoverage)
		for y := -8; y < 12; y += tileH {
			for x := -8; x < 12; x += tileW {
				part, err := RasterizeBlock(tri, Rect{
					MinX: x,
					MaxX: x + tileW - 1,
					MinY: y,
					MaxY: y + tileH - 1,
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range part {
					k := cellKey(c)
					if _, dup := got[k]; dup {
						t.Fatalf("iter %d: overlapping blocks duplicated cell %v", iter, k)
					}
					got[k] = c
				}
			}
		}

		if len(got) != len(want) {
			t.Fatalf("iter %d: block cells %d != whole cells %d", iter, len(got), len(want))
		}
		for k, w := range want {
			g, ok := got[k]
			if !ok ||
				g.Area.Cmp(w.Area) != 0 ||
				g.Centroid.U.Cmp(w.Centroid.U) != 0 ||
				g.Centroid.V.Cmp(w.Centroid.V) != 0 ||
				g.Bary[0].Cmp(w.Bary[0]) != 0 ||
				g.Bary[1].Cmp(w.Bary[1]) != 0 ||
				g.Bary[2].Cmp(w.Bary[2]) != 0 ||
				g.PrimitiveID != w.PrimitiveID {
				t.Fatalf("iter %d: block result mismatch at %v", iter, k)
			}
		}
	}
}

// TestBlockOutsideTriangle 分块完全在三角形外时返回空且无错误。
func TestBlockOutsideTriangle(t *testing.T) {
	tri := Triangle{A: ipt(0, 0), B: ipt(1, 0), C: ipt(0, 1)}
	cov, err := RasterizeBlock(tri, Rect{MinX: 50, MaxX: 60, MinY: 50, MaxY: 60})
	if err != nil {
		t.Fatal(err)
	}
	if cov != nil {
		t.Fatalf("outside block must be empty, got %d cells", len(cov))
	}
}

// TestInputNotMutated 即使多个顶点共享同一个 *big.Rat，光栅化也不得
// 改写调用方的输入（big.Rat 运算方法默认会原地修改接收者）。
func TestInputNotMutated(t *testing.T) {
	shared := rfrac(1, 1)
	tri := Triangle{
		A: RatPoint{U: rfrac(0, 1), V: rfrac(0, 1)},
		B: RatPoint{U: new(big.Rat).Set(shared), V: rfrac(0, 1)},
		C: RatPoint{U: rfrac(0, 1), V: new(big.Rat).Set(shared)},
	}
	snapshot := []string{
		tri.A.U.RatString(), tri.A.V.RatString(),
		tri.B.U.RatString(), tri.B.V.RatString(),
		tri.C.U.RatString(), tri.C.V.RatString(),
	}
	if _, err := Rasterize(tri); err != nil {
		t.Fatal(err)
	}
	got := []string{
		tri.A.U.RatString(), tri.A.V.RatString(),
		tri.B.U.RatString(), tri.B.V.RatString(),
		tri.C.U.RatString(), tri.C.V.RatString(),
	}
	for i := range snapshot {
		if got[i] != snapshot[i] {
			t.Fatalf("input component %d mutated: %s -> %s", i, snapshot[i], got[i])
		}
	}
	if shared.RatString() != "1" {
		t.Fatalf("shared rational mutated to %s", shared.RatString())
	}
}

// TestBlockClippedToBounds 分块矩形与包围盒部分相交时，只返回交集部分，
// 且与整图结果中的对应方格逐格相等。
func TestBlockClippedToBounds(t *testing.T) {
	tri := Triangle{
		A: rpt(1, 3, 1, 3),
		B: rpt(7, 3, 1, 3),
		C: rpt(1, 3, 7, 3),
	}
	whole, err := Rasterize(tri)
	if err != nil {
		t.Fatal(err)
	}
	want := coverageMap(t, whole)

	part, err := RasterizeBlock(tri, Rect{MinX: 1, MaxX: 1, MinY: -5, MaxY: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range part {
		w, ok := want[cellKey(c)]
		if !ok || c.Area.Cmp(w.Area) != 0 {
			t.Fatalf("clipped block mismatch at %v", cellKey(c))
		}
	}
	if len(part) == 0 || len(part) >= len(want) {
		t.Fatalf("expected a strict subset, got %d of %d cells", len(part), len(want))
	}
}
