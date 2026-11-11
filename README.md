# 安全断言插值摘要

安全测试得到一条不可达漏洞路径，需要提炼阻断相似路径的条件。实现供Go分析服务调用的有限布尔插值包，输入前后两组CNF及共享变量，若合取不可满足，返回仅使用共享变量且被前组蕴含、与后组矛盾的公式；最多十二个共享变量。独立枚举验证两项性质，输入可满足时返回见证，不伪造安全证明。

## 用法

```go
import "example.org/netsec/q0382"

// 文字编码：MakeLit(v) 为正文字，Neg() 取反。
x0, x1 := netsec.MakeLit(0), netsec.MakeLit(1)

// 前组 A（路径前缀约束）与后组 B（路径后缀/漏洞约束），均为 CNF。
a := netsec.CNF{{x0, x1}, {x0, x1.Neg()}} // 强制 x0=true
b := netsec.CNF{{x0.Neg()}}               // 强制 x0=false

res, err := netsec.Interpolate(a, b, []int{0})
if err != nil {
	// 仅在输入非法时返回错误（变量越界、共享变量缺失或超过 12 个等）。
	log.Fatal(err)
}

if res.SAT {
	// A&B 可满足：返回具体见证，绝不产生插值“证明”。
	fmt.Println("jointly satisfiable, witness:", res.Witness)
} else {
	// A&B 不可满足：插值项只引用共享变量。
	fmt.Println("interpolant:", res.Interpolant.String())
	fmt.Println("verified:   ", res.Verification.Passed)
}
```

## 正确性边界

- `Interpolate` 枚举全部 `2**n` 个共享变量赋值（`n <= MaxSharedVars`），保留所有可
  扩展为 A 的模型的赋值，构造为完整合取项的析取（DNF）。
- `VerifyInterpolant` 独立重新枚举并对每个赋值分别重跑内置 DPLL 求解器，检查：
  - 蕴含性质 `A |= I`（A 可行的共享赋值必满足 I）；
  - 矛盾性质 `B & I` 不可满足（满足 I 的赋值对 B 不可行）；
  - 语言限制：I 只引用声明的共享变量；
  - 前置条件：`A & B` 确实不可满足。
- 可满足输入走 `VerifyWitness`，直接对子句求值校验见证，不返回插值项。

## 验证

```sh
go test ./...        # 含 200 组随机差分与 12 变量边界用例
go test -race ./...
go test -bench=. -run=^$ .
```
