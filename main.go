package main

import "fmt"

// demoFuncTable 是示例输入：各函数的读写集合与是否抛错。
func demoFuncTable() FuncTable {
	return FuncTable{
		"f": {Reads: []string{"x"}, Writes: []string{"x"}}, // 读 x、写 x
		"g": {Throws: true},                                // 调用即抛错
		"h": {Reads: []string{"x"}},                        // 纯读 x
		"q": {Writes: []string{"y"}},                       // 只写 y
	}
}

type scenario struct {
	name string
	prog []Stmt
	e    Expr
}

func scenarios() []scenario {
	h := Call{Fn: "h"}
	g := Call{Fn: "g"}
	q := Call{Fn: "q"}
	return []scenario{
		{
			name: "1. if/else 两分支各出现一次（纯读）——允许",
			prog: []Stmt{
				If{Cond: Var{"x"},
					Then: []Stmt{Assign{"y", h}},
					Else: []Stmt{Assign{"y", h}}},
			},
			e: h,
		},
		{
			name: "2. 短路互补（&& 与 ||）——每条路径恰好求值一次，允许",
			prog: []Stmt{
				Assign{"y", And{Var{"x"}, h}},
				Assign{"y", Or{Var{"x"}, h}},
			},
			e: h,
		},
		{
			name: "3. 短路同向（&& 与 &&）——x 非零时求值两次，拒绝",
			prog: []Stmt{
				ExprStmt{And{Var{"x"}, h}},
				ExprStmt{And{Var{"x"}, h}},
			},
			e: h,
		},
		{
			name: "4. 两次出现之间变量被写——读取版本改变，拒绝",
			prog: []Stmt{
				If{Cond: Var{"x"},
					Then: []Stmt{Assign{"y", h}},
					Else: []Stmt{Assign{"x", Num{9}}, Assign{"y", h}}},
			},
			e: h,
		},
		{
			name: "5. 顺序两次求值（纯表达式）——求值次数 2 变 1，拒绝",
			prog: []Stmt{
				Assign{"y", h},
				Assign{"y", h},
			},
			e: h,
		},
		{
			name: "6. e 会抛错且两分支互斥——首错一致，允许",
			prog: []Stmt{
				If{Cond: Var{"x"},
					Then: []Stmt{ExprStmt{g}},
					Else: []Stmt{ExprStmt{g}}},
			},
			e: g,
		},
		{
			name: "7. e 抛错且绑定点与求值点之间有写——异常提前跳过写，拒绝",
			prog: []Stmt{
				ExprStmt{Add{q, g}},
				ExprStmt{g},
			},
			e: g,
		},
		{
			name: "8. e 写 y，绑定点与求值点之间 y 被读——反依赖，拒绝",
			prog: []Stmt{
				If{Cond: Var{"x"},
					Then: []Stmt{ExprStmt{q}},
					Else: []Stmt{Assign{"x", Var{"y"}}, ExprStmt{q}}},
			},
			e: q,
		},
	}
}

func main() {
	ft := demoFuncTable()
	for _, sc := range scenarios() {
		fmt.Println("== " + sc.name)
		fmt.Println("   程序: " + ProgString(sc.prog))
		fmt.Println("   抽取: e = " + ExprString(sc.e))
		allow, viols, ref, err := CheckExtract(sc.prog, ft, sc.e)
		if err != nil {
			fmt.Println("   错误: " + err.Error())
			continue
		}
		fmt.Println("   变换: " + ProgString(ref))
		if allow {
			fmt.Println("   判定: 允许抽取（求值次数 / 首错 / 读取版本均保持）")
		} else {
			fmt.Println("   判定: 拒绝")
			for _, v := range viols {
				fmt.Println("   " + v.String())
			}
		}
		eq, why := equivalent(sc.prog, ref, ft, sc.e)
		if eq {
			fmt.Println("   对照: 解释器枚举确认两程序行为一致")
		} else {
			fmt.Println("   对照: 解释器发现行为差异 -> " + why)
		}
		fmt.Println()
	}
}
