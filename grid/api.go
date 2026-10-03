package grid

import (
	"errors"
	"fmt"
	"math"
)

// baseCellScale 分辨率 0 时六边形中心间距（切平面单位，1 单位 = 地球半径）。
// 取值保证整个二十面体面（球心投影半径约 0.764）落在单基单元的
// 分形瓦片覆盖范围内。
const baseCellScale = 2.0

// resScale 分辨率 res 下的六边形中心间距（切平面单位）。
func resScale(res int) float64 { return baseCellScale / math.Pow(7, float64(res)/2) }

// rotAngle 层级旋转角 = arg(2+ω) ≈ 19.107°。
// aperture-7 层级中，每细一级格网相对旋转该角（H3 的 Class II/III 交替同理），
// 编码/解码必须按分辨率补偿旋转，父子单元才能在地理上嵌套。
var rotAngle = math.Atan2(math.Sqrt(3)/2, 2.5)

// rotateRes 将平面坐标旋转 res*rotAngle（编码方向）。
func rotateRes(x, y float64, res int) (float64, float64) {
	if res == 0 {
		return x, y
	}
	cs, sn := math.Cos(float64(res)*rotAngle), math.Sin(float64(res)*rotAngle)
	return x*cs - y*sn, x*sn + y*cs
}

// unrotateRes rotateRes 的逆变换（解码方向）。
func unrotateRes(x, y float64, res int) (float64, float64) {
	if res == 0 {
		return x, y
	}
	cs, sn := math.Cos(float64(res)*rotAngle), math.Sin(float64(res)*rotAngle)
	return x*cs + y*sn, -x*sn + y*cs
}

// CellAreaM2 分辨率 res 下单元的平均面积（平方米）。
func CellAreaM2(res int) float64 {
	s := resScale(res) * EarthRadiusM
	return math.Sqrt(3) / 2 * s * s
}

// latLngToAxial 经纬度 -> (基单元, 面内轴向整数格点)。
func latLngToAxial(ll LatLng, res int) (int, axial, error) {
	if res < 0 || res > MaxRes {
		return 0, axial{}, ErrInvalidResolution
	}
	if math.IsNaN(ll.Lat) || math.IsNaN(ll.Lng) || ll.Lat < -90 || ll.Lat > 90 {
		return 0, axial{}, fmt.Errorf("grid: invalid latlng %v", ll)
	}
	lng := math.Remainder(ll.Lng, 360)
	p := (LatLng{Lat: ll.Lat, Lng: lng}).toVec3()
	f := selectFace(p)
	x, y := projectFace(f, p)
	x, y = rotateRes(x, y, res) // 层级旋转补偿
	s := resScale(res)
	// 平面坐标 -> 轴向浮点坐标 -> 立方取整得最近六边形中心
	beta := 2 * y / (s * math.Sqrt(3))
	alpha := x/s - beta/2
	q, r := cubeRound(alpha, beta)
	return f, axial{q, r}, nil
}

// cubeRound 立方坐标取整到最近六边形中心，返回轴向坐标。
func cubeRound(a, b float64) (int64, int64) {
	c := -a - b
	ra, rb, rc := math.Round(a), math.Round(b), math.Round(c)
	da, db, dc := math.Abs(ra-a), math.Abs(rb-b), math.Abs(rc-c)
	switch {
	case da > db && da > dc:
		ra = -rb - rc
	case db > dc:
		rb = -ra - rc
	}
	return int64(ra), int64(rb)
}

// FromLatLng 将 GPS 坐标编码为分辨率 res 的网格索引。
func FromLatLng(ll LatLng, res int) (Index, error) {
	f, t, err := latLngToAxial(ll, res)
	if err != nil {
		return 0, err
	}
	return axialToIndex(f, res, t)
}

// axialToIndex 面内格点 -> 索引。digit 展开超过 res 级时（面边缘兜底）
// 截断为祖先单元，保证编码总能成功。
func axialToIndex(baseCell, res int, t axial) (Index, error) {
	digits := expand(t)
	if len(digits) > res {
		digits = digits[:res]
	}
	padded := make([]int, res)
	copy(padded[res-len(digits):], digits)
	return makeIndex(res, baseCell, padded), nil
}

// localAxial 索引 -> 面内轴向整数格点（纯整数，无浮点）。
func localAxial(h Index) axial { return collapse(h.digits()) }

// Center 返回单元中心的经纬度。
func Center(h Index) LatLng {
	res := h.Res()
	t := localAxial(h)
	s := resScale(res)
	x := s * (float64(t.q) + float64(t.r)/2)
	y := s * (float64(t.r) * math.Sqrt(3) / 2)
	x, y = unrotateRes(x, y, res) // 层级旋转补偿
	return vec3ToLatLng(unprojectFace(h.BaseCell(), x, y))
}

// Parent 返回分辨率 res（<= 当前）的父单元。
func Parent(h Index, res int) (Index, error) {
	if res < 0 || res > h.Res() {
		return 0, ErrInvalidResolution
	}
	return makeIndex(res, h.BaseCell(), h.digits()[:res]), nil
}

// Child 返回指定方向 digit(0..6) 的下一级子单元。
func Child(h Index, digit int) (Index, error) {
	if h.Res() >= MaxRes {
		return 0, ErrInvalidResolution
	}
	if digit < 0 || digit > 6 {
		return 0, errors.New("grid: invalid digit")
	}
	digits := append(h.digits(), digit)
	return makeIndex(h.Res()+1, h.BaseCell(), digits), nil
}

// Children 返回全部 7 个子单元。
func Children(h Index) ([]Index, error) {
	if h.Res() >= MaxRes {
		return nil, ErrInvalidResolution
	}
	children := make([]Index, 0, 7)
	for d := 0; d < 7; d++ {
		c, err := Child(h, d)
		if err != nil {
			return nil, err
		}
		children = append(children, c)
	}
	return children, nil
}
