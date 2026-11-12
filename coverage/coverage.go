// Package coverage 实现 UV 图集上三角形对像素方格的保守精确覆盖计算。
//
// 全部几何运算使用 math/big.Rat 精确有理算术：对每个与三角形包围盒相交的
// 像素方格，用 Sutherland–Hodgman 半平面裁剪求方格与三角形的交多边形，
// 再由多边形的有理坐标精确求面积（覆盖比例）与面积质心，并把质心换算成
// 相对于该三角形三个顶点的重心坐标。
//
// 共享边语义：两个图元的共享边落在方格内部时只是一条零面积接触，裁剪
// 结果退化为面积为零的多边形，本包直接跳过，因此共享边不会向任何像素
// 重复贡献面积。
//
// 重叠 UV 语义：本包无全局状态，每次调用只描述一个三角形，不合并、不
// 平均任何结果。重叠 UV 上不同图元的覆盖由调用方通过各自的 PixelCoverage
// 分别保留与累加。
package coverage

import (
	"errors"
	"math/big"
)

// ErrDegenerate 表示三角形退化（三个 UV 顶点共线或重合），面积为零，
// 对任何像素都没有贡献。
var ErrDegenerate = errors.New("coverage: degenerate triangle has zero area")

// ErrOutOfRange 表示 UV 坐标的整数部分无法在本机 int 范围内表示，
// 无法枚举像素方格。
var ErrOutOfRange = errors.New("coverage: uv coordinate out of int range")

// RatPoint 是精确有理 UV 坐标。字段指针必须为非 nil 的有效 *big.Rat。
type RatPoint struct {
	U *big.Rat
	V *big.Rat
}

// NewRatPoint 以有理分量构造一个 UV 点，构造时复制传入值。
func NewRatPoint(u, v *big.Rat) RatPoint {
	return RatPoint{U: new(big.Rat).Set(u), V: new(big.Rat).Set(v)}
}

// NewRatPointInt64 以整数 UV 构造一个有理点，便于测试与管线适配层使用。
func NewRatPointInt64(u, v int64) RatPoint {
	return RatPoint{U: big.NewRat(u, 1), V: big.NewRat(v, 1)}
}

// Triangle 是 UV 空间中的一个三角形，附带调用方分配的图元标识。
// 顶点方向（顺/逆时针）不影响结果，内部会统一归一化。
type Triangle struct {
	A, B, C RatPoint

	// PrimitiveID 由调用方提供，原样透传到结果中。重叠 UV 的不同图元
	// 应使用不同标识，本包不会合并它们。
	PrimitiveID uint64
}

// PixelCoverage 是一个像素方格与一个三角形的精确相交结果。
type PixelCoverage struct {
	// CellX、CellY 是像素方格的整数下标，方格覆盖
	// [CellX, CellX+1] × [CellY, CellY+1]。
	CellX int
	CellY int

	// Area 是交多边形的精确面积。单位方格面积为 1，因此它同时也是
	// 该像素的覆盖比例，取值范围 (0,1]；零面积接触不会产生结果项。
	Area *big.Rat

	// Centroid 是交多边形的面积质心理想 UV 坐标。
	Centroid RatPoint

	// Bary 是质心相对于三角形 A、B、C 的重心坐标，三者之和恒为 1。
	Bary [3]*big.Rat

	// PrimitiveID 透传自输入三角形，用于在重叠 UV 上区分图元。
	PrimitiveID uint64
}

// CoverageFloat64 返回覆盖比例最接近的 float64 表示。
func (p PixelCoverage) CoverageFloat64() float64 { return ratFloat64(p.Area) }

// CentroidFloat64 返回面积质心的 float64 表示。
func (p PixelCoverage) CentroidFloat64() (u, v float64) {
	return ratFloat64(p.Centroid.U), ratFloat64(p.Centroid.V)
}

// BaryFloat64 返回重心坐标的 float64 表示。
func (p PixelCoverage) BaryFloat64() (a, b, c float64) {
	return ratFloat64(p.Bary[0]), ratFloat64(p.Bary[1]), ratFloat64(p.Bary[2])
}

// ratFloat64 丢弃 big.Rat.Float64 的精确性标志，只取最接近的浮点值。
func ratFloat64(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// Rect 是像素下标空间中的闭区间矩形：x 方向枚举 [MinX, MaxX]，
// y 方向枚举 [MinY, MaxY]，边界均含。
type Rect struct {
	MinX, MaxX int
	MinY, MaxY int
}

// 常用精确常量，避免重复分配。
var (
	ratOne   = big.NewRat(1, 1)
	ratTwo   = big.NewRat(2, 1)
	ratThree = big.NewRat(3, 1)
)

// Rasterize 对三角形包围盒覆盖到的全部像素方格做保守精确光栅化。
// 退化三角形返回 ErrDegenerate，且不产生任何像素贡献。
func Rasterize(tri Triangle) ([]PixelCoverage, error) {
	box, doubleArea, err := prepare(tri)
	if err != nil {
		return nil, err
	}
	return rasterizeBox(tri, box, doubleArea), nil
}

// RasterizeBlock 只处理 rect 内的像素方格（分块入口）。它与 Rasterize
// 使用完全相同的逐方格计算：把整张图集切成互不重叠的分块分别调用，
// 拼接结果与一次性 Rasterize 完全一致。落在三角形外的方格不产生结果项，
// 因此分块边界无需任何特殊处理。
func RasterizeBlock(tri Triangle, rect Rect) ([]PixelCoverage, error) {
	if rect.MinX > rect.MaxX || rect.MinY > rect.MaxY {
		return nil, errors.New("coverage: empty block rect")
	}
	box, doubleArea, err := prepare(tri)
	if err != nil {
		return nil, err
	}

	block := Rect{
		MinX: max(rect.MinX, box.MinX),
		MaxX: min(rect.MaxX, box.MaxX),
		MinY: max(rect.MinY, box.MinY),
		MaxY: min(rect.MaxY, box.MaxY),
	}
	if block.MinX > block.MaxX || block.MinY > block.MaxY {
		return nil, nil
	}
	return rasterizeBox(tri, block, doubleArea), nil
}

// triangleEdge 是归一化方向后的有向边，内部恒为逆时针，
// 内部点满足 cross(E, P-S) >= 0。
type triangleEdge struct {
	sx, sy *big.Rat
	ex, ey *big.Rat
}

// prepare 校验三角形、归一化绕序、计算二倍有向面积并返回像素包围盒。
func prepare(tri Triangle) (Rect, *big.Rat, error) {
	if tri.A.U == nil || tri.A.V == nil || tri.B.U == nil || tri.B.V == nil ||
		tri.C.U == nil || tri.C.V == nil {
		return Rect{}, nil, errors.New("coverage: triangle contains nil uv component")
	}

	// 深拷贝输入：big.Rat 的运算方法会原地修改接收者，而调用方可能让
	// 多个顶点（或多个三角形）共享同一个 *big.Rat。拷贝后本包的全部
	// 中间运算都不会改写调用方数据，也不会因别名互相污染。
	tri.A = clonePoint(tri.A)
	tri.B = clonePoint(tri.B)
	tri.C = clonePoint(tri.C)

	doubleArea := cross2(tri.A, tri.B, tri.C)
	if doubleArea.Sign() == 0 {
		return Rect{}, nil, ErrDegenerate
	}

	a, b, c := tri.A, tri.B, tri.C
	if doubleArea.Sign() < 0 {
		// 交换 B、C 使绕序变为逆时针，同时保持面积绝对值不变。
		b, c = c, b
		doubleArea = new(big.Rat).Neg(doubleArea)
	}

	box, err := pixelBounds(a, b, c)
	if err != nil {
		return Rect{}, nil, err
	}
	return box, doubleArea, nil
}

func clonePoint(p RatPoint) RatPoint {
	return RatPoint{
		U: new(big.Rat).Set(p.U),
		V: new(big.Rat).Set(p.V),
	}
}

// rasterizeBox 枚举 box 内每个单位方格，裁剪求交并输出非零覆盖。
func rasterizeBox(tri Triangle, box Rect, doubleArea *big.Rat) []PixelCoverage {
	a, b, c := tri.A, tri.B, tri.C
	if cross2(a, b, c).Sign() < 0 {
		b, c = c, b
	}
	edges := []triangleEdge{
		{sx: a.U, sy: a.V, ex: b.U, ey: b.V},
		{sx: b.U, sy: b.V, ex: c.U, ey: c.V},
		{sx: c.U, sy: c.V, ex: a.U, ey: a.V},
	}

	var out []PixelCoverage
	for cy := box.MinY; cy <= box.MaxY; cy++ {
		for cx := box.MinX; cx <= box.MaxX; cx++ {
			poly := clipCell(cx, cy, edges)
			area, centroid, ok := polygonAreaCentroid(poly)
			if !ok {
				// 只在边或顶点处接触：零面积，不贡献。
				continue
			}
			bary := barycentric(a, b, c, centroid, doubleArea)
			out = append(out, PixelCoverage{
				CellX:       cx,
				CellY:       cy,
				Area:        area,
				Centroid:    centroid,
				Bary:        bary,
				PrimitiveID: tri.PrimitiveID,
			})
		}
	}
	return out
}

// clipCell 用三角形的三条有向边依次裁剪单位方格
// [x,x+1] × [y,y+1]，返回交多边形顶点（逆时针，可能含共线点）。
func clipCell(x, y int, edges []triangleEdge) []RatPoint {
	xf := big.NewRat(int64(x), 1)
	yf := big.NewRat(int64(y), 1)
	poly := []RatPoint{
		{U: new(big.Rat).Set(xf), V: new(big.Rat).Set(yf)},
		{U: new(big.Rat).Add(xf, ratOne), V: new(big.Rat).Set(yf)},
		{U: new(big.Rat).Add(xf, ratOne), V: new(big.Rat).Add(yf, ratOne)},
		{U: new(big.Rat).Set(xf), V: new(big.Rat).Add(yf, ratOne)},
	}

	for _, edge := range edges {
		if len(poly) == 0 {
			return nil
		}
		poly = clipHalfPlane(poly, edge)
	}
	return poly
}

// clipHalfPlane 对单个半平面 cross(E, P-S) >= 0 做 Sutherland–Hodgman 裁剪。
func clipHalfPlane(poly []RatPoint, edge triangleEdge) []RatPoint {
	dx := new(big.Rat).Sub(edge.ex, edge.sx)
	dy := new(big.Rat).Sub(edge.ey, edge.sy)

	signed := func(p RatPoint) *big.Rat {
		// cross(E, P-S) = dx*(py-sy) - dy*(px-sx)
		px := new(big.Rat).Sub(p.U, edge.sx)
		py := new(big.Rat).Sub(p.V, edge.sy)
		return new(big.Rat).Sub(new(big.Rat).Mul(dx, py), new(big.Rat).Mul(dy, px))
	}

	result := make([]RatPoint, 0, len(poly)+1)
	prev := poly[len(poly)-1]
	prevInside := signed(prev).Sign() >= 0
	for _, cur := range poly {
		curInside := signed(cur).Sign() >= 0

		switch {
		case curInside && prevInside:
			result = append(result, cur)
		case curInside && !prevInside:
			result = append(result, intersectEdge(prev, cur, edge, dx, dy))
			result = append(result, cur)
		case !curInside && prevInside:
			result = append(result, intersectEdge(prev, cur, edge, dx, dy))
		}

		prev = cur
		prevInside = curInside
	}
	return result
}

// intersectEdge 求线段 P->Q 与支撑直线 cross(E, X-S)=0 的交点。
// 仅在两端符号不同（分母非零）时调用，结果恒为精确有理点。
func intersectEdge(p, q RatPoint, edge triangleEdge, dx, dy *big.Rat) RatPoint {
	// f(X) = dx*(Xy-sy) - dy*(Xx-sx)，沿 P->Q 线性变化。
	fp := new(big.Rat).Sub(
		new(big.Rat).Mul(dx, new(big.Rat).Sub(p.V, edge.sy)),
		new(big.Rat).Mul(dy, new(big.Rat).Sub(p.U, edge.sx)),
	)
	fq := new(big.Rat).Sub(
		new(big.Rat).Mul(dx, new(big.Rat).Sub(q.V, edge.sy)),
		new(big.Rat).Mul(dy, new(big.Rat).Sub(q.U, edge.sx)),
	)
	// t = -fp / (fq - fp)，0 < t < 1。
	t := new(big.Rat).Quo(new(big.Rat).Neg(fp), new(big.Rat).Sub(fq, fp))

	u := new(big.Rat).Add(p.U, new(big.Rat).Mul(t, new(big.Rat).Sub(q.U, p.U)))
	v := new(big.Rat).Add(p.V, new(big.Rat).Mul(t, new(big.Rat).Sub(q.V, p.V)))
	return RatPoint{U: u, V: v}
}

// polygonAreaCentroid 用精确有理的鞋带公式计算多边形面积与面积质心。
// ok 为 false 表示多边形退化（点、线段或空），面积为零。
func polygonAreaCentroid(poly []RatPoint) (area *big.Rat, centroid RatPoint, ok bool) {
	n := len(poly)
	if n < 3 {
		return nil, RatPoint{}, false
	}

	twiceArea := new(big.Rat) // 有向二倍面积
	sumCX := new(big.Rat)     // sum (xi+xi+1)*cross_i
	sumCY := new(big.Rat)

	for i := 0; i < n; i++ {
		p := poly[i]
		q := poly[(i+1)%n]
		cross := new(big.Rat).Sub(
			new(big.Rat).Mul(p.U, q.V),
			new(big.Rat).Mul(p.V, q.U),
		)
		twiceArea.Add(twiceArea, cross)
		sumCX.Add(sumCX, new(big.Rat).Mul(new(big.Rat).Add(p.U, q.U), cross))
		sumCY.Add(sumCY, new(big.Rat).Mul(new(big.Rat).Add(p.V, q.V), cross))
	}

	if twiceArea.Sign() <= 0 {
		// 逆时针裁剪结果应为正；零或负说明退化为线/点接触。
		return nil, RatPoint{}, false
	}

	area = new(big.Rat).Quo(twiceArea, ratTwo)
	// 质心 C = (1/(6A)) * sum (P+Q)*cross = sum / (3 * twiceArea)。
	den := new(big.Rat).Mul(ratThree, twiceArea)
	centroid = RatPoint{
		U: new(big.Rat).Quo(sumCX, den),
		V: new(big.Rat).Quo(sumCY, den),
	}
	return area, centroid, true
}

// barycentric 求点 P 相对于逆时针三角形 A、B、C 的精确重心坐标。
// doubleArea 是三角形二倍面积（正数），调用方已保证三角形非退化。
func barycentric(a, b, c, p RatPoint, doubleArea *big.Rat) [3]*big.Rat {
	// 逆时针三角形中，重心分量等于对边与 P 构成的子三角形有向面积占比：
	// alpha 对应边 BC，beta 对应边 CA，gamma 对应边 AB。
	alpha := new(big.Rat).Quo(cross2(b, c, p), doubleArea)
	beta := new(big.Rat).Quo(cross2(c, a, p), doubleArea)
	gamma := new(big.Rat).Quo(cross2(a, b, p), doubleArea)
	return [3]*big.Rat{alpha, beta, gamma}
}

// cross2 返回 cross(B-A, C-A) 的精确有向二倍面积。
func cross2(a, b, c RatPoint) *big.Rat {
	abx := new(big.Rat).Sub(b.U, a.U)
	aby := new(big.Rat).Sub(b.V, a.V)
	acx := new(big.Rat).Sub(c.U, a.U)
	acy := new(big.Rat).Sub(c.V, a.V)
	return new(big.Rat).Sub(
		new(big.Rat).Mul(abx, acy),
		new(big.Rat).Mul(aby, acx),
	)
}

// pixelBounds 返回与三个顶点轴对齐包围盒相交的全部单位像素方格下标。
// 方格以整数网格线为界，边界顶点只会让相邻方格产生零面积接触而被跳过。
func pixelBounds(a, b, c RatPoint) (Rect, error) {
	minU := minRat(a.U, b.U, c.U)
	maxU := maxRat(a.U, b.U, c.U)
	minV := minRat(a.V, b.V, c.V)
	maxV := maxRat(a.V, b.V, c.V)

	minX, err := floorInt(minU)
	if err != nil {
		return Rect{}, err
	}
	maxX, err := ceilInt(maxU)
	if err != nil {
		return Rect{}, err
	}
	minY, err := floorInt(minV)
	if err != nil {
		return Rect{}, err
	}
	maxY, err := ceilInt(maxV)
	if err != nil {
		return Rect{}, err
	}

	// 顶点恰在网格线上时 ceil(max) 会多取一个方格；该方格仅零面积接触，
	// 裁剪后自然被丢弃，这里保守保留即可，不漏掉任何细小面。
	return Rect{MinX: minX, MaxX: maxX, MinY: minY, MaxY: maxY}, nil
}

// floorInt 返回不大于 r 的最大整数，超出 int 范围时返回 ErrOutOfRange。
func floorInt(r *big.Rat) (int, error) {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if r.Num().Sign() < 0 && new(big.Int).Rem(r.Num(), r.Denom()).Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, ErrOutOfRange
	}
	v := q.Int64()
	if int64(int(v)) != v {
		return 0, ErrOutOfRange
	}
	return int(v), nil
}

// ceilInt 返回不小于 r 的最小整数，超出 int 范围时返回 ErrOutOfRange。
func ceilInt(r *big.Rat) (int, error) {
	neg := new(big.Rat).Neg(r)
	f, err := floorInt(neg)
	if err != nil {
		return 0, err
	}
	return -f, nil
}

func minRat(values ...*big.Rat) *big.Rat {
	m := values[0]
	for _, v := range values[1:] {
		if v.Cmp(m) < 0 {
			m = v
		}
	}
	return m
}

func maxRat(values ...*big.Rat) *big.Rat {
	m := values[0]
	for _, v := range values[1:] {
		if v.Cmp(m) > 0 {
			m = v
		}
	}
	return m
}
