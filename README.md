# Algebraic Number Comparator

这是一个仅使用 Go 标准库实现的实代数数比较与符号判定器。它支持有理数、加、减、乘、除以及非负代数数平方根组成的表达式，不使用浮点近似作出最终判定。

## 算法

1. 用表达式树精确表示代数数。
2. 对加、减、乘、除通过 Sylvester resultant 构造零化多项式；对 `sqrt(a)` 将 `a` 的零化多项式 `p(x)` 复合为 `p(x^2)`。
3. 使用 `math/big.Rat` 做有理系数多项式运算。
4. 对零化多项式取平方自由核，并用 Kronecker 插值法在 `Q[x]` 中完成不可约因式分解。
5. 用有理区间算术逐步精化表达式区间，从不可约因子中选出极小多项式。
6. 对极小多项式构造 Sturm 序列，隔离全部实根，并按实根序号确定表达式的规范根。
7. 比较两个数时，先比较规范极小多项式和根序号；不同根则继续二分隔离区间，直到区间严格分离。

因此，`sqrt(2) + sqrt(3)` 与 `sqrt(5 + 2*sqrt(6))` 会得到同一个极小多项式 `x^4 - 10*x^2 + 1` 和同一个实根隔离区间，从而精确输出相等。

## 运行样例

```sh
go run ./cmd/algebraic-demo
```

输出：

```text
nested radical identity: (sqrt(2) + sqrt(3)) = sqrt((5 + (2 * sqrt(6))))
  left minimal polynomial:  x^4 - 10*x^2 + 1
  right minimal polynomial: x^4 - 10*x^2 + 1
  left isolating interval:  [3, 9/2]
  right isolating interval: [3, 9/2]

strict radical comparison: (sqrt(2) + sqrt(3)) < sqrt(10)
  left minimal polynomial:  x^4 - 10*x^2 + 1
  right minimal polynomial: x^2 - 10
  left isolating interval:  [3, 9/2]
  right isolating interval: [0, 12]
```

## API 示例

```go
sqrt2 := algebraic.MustSqrt(algebraic.Int(2))
sqrt3 := algebraic.MustSqrt(algebraic.Int(3))
sqrt6 := algebraic.MustSqrt(algebraic.Int(6))

left := algebraic.Add(sqrt2, sqrt3)
right := algebraic.MustSqrt(algebraic.Add(
    algebraic.Int(5),
    algebraic.Mul(algebraic.Int(2), sqrt6),
))

fmt.Println(left.Compare(right)) // 0
fmt.Println(left.Sign())         // 1
fmt.Println(left.MinimalPolynomial())
```

`Compare` 返回 `-1`、`0`、`1`；`Sign` 返回 `-1`、`0`、`1`。可能失败的构造使用 `Sqrt` 和 `Div`，已知安全时可使用 `MustSqrt` 和 `MustDiv`。

## 测试

```sh
go test -v ./...
```

当前测试结果：

```text
=== RUN   TestNestedRadicalEquality
--- PASS: TestNestedRadicalEquality (0.01s)
=== RUN   TestStrictRadicalComparison
--- PASS: TestStrictRadicalComparison (0.00s)
=== RUN   TestExactSignAndZero
--- PASS: TestExactSignAndZero (0.00s)
=== RUN   TestRationalAndFieldOperations
--- PASS: TestRationalAndFieldOperations (0.00s)
=== RUN   TestMinimalPolynomialCoefficients
--- PASS: TestMinimalPolynomialCoefficients (0.00s)
=== RUN   TestPolynomialFactorization
--- PASS: TestPolynomialFactorization (0.00s)
=== RUN   TestSturmRootIsolation
--- PASS: TestSturmRootIsolation (0.00s)
PASS
ok  	example.com/algebraic	2.940s
```
