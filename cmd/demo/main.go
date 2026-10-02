// Command demo builds a Delaunay triangulation of 50 random survey points
// and prints the resulting mesh: triangle adjacency, convex hull, and a
// verification of the empty circumcircle property.
package main

import (
	"fmt"
	"math/rand"

	"delaunay"
)

func main() {
	r := rand.New(rand.NewSource(42))
	pts := make([]delaunay.Point, 50)
	for i := range pts {
		pts[i] = delaunay.Point{X: 100 * r.Float64(), Y: 100 * r.Float64()}
	}

	tri := delaunay.NewBuilder(pts...).Triangulation()
	hull := tri.ConvexHull()

	fmt.Println("=== Delaunay 三角剖分演示 ===")
	fmt.Printf("输入点数:        %d (种子 42, 均匀分布于 [0,100)²)\n", len(tri.Points))
	fmt.Printf("三角形数:        %d (Euler 公式 2n-2-h = %d)\n",
		len(tri.Tris), 2*len(tri.Points)-2-len(hull))
	fmt.Printf("凸包顶点 (%d, 逆时针): %v\n", len(hull), hull)
	fmt.Println("三角形邻接表 (V=顶点, N=对边邻居, -1=凸包边界):")
	for i, tr := range tri.Tris {
		fmt.Printf("  T%-3d V=[%2d %2d %2d]  N=[%2d %2d %2d]\n",
			i, tr.V[0], tr.V[1], tr.V[2], tr.N[0], tr.N[1], tr.N[2])
	}
	if tri.IsDelaunay() {
		fmt.Printf("空外接圆性质:    PASS (已验证全部 %d 个三角形)\n", len(tri.Tris))
	} else {
		fmt.Println("空外接圆性质:    FAIL")
	}
}
