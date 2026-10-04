# polygcd

有理系数一元多项式的 GCD / 扩展 GCD 库（Go）。

## 表示与约定

- `Poly` 为 `[]*big.Rat`（低次到高次），系数是**约分有理数**（`math/big.Rat` 自动约分）。
- 零多项式为空切片，次数定义为 `-1`。
- 非零输入的 GCD 一律返回**首一**（monic）多项式。
- 退化情形：
  - `GCD(0, 0) = 0`，贝祖系数 `S = T = 0`；
  - `GCD(a, 0) = monic(a)`，`S = 1/lc(a), T = 0`（对称同理）；
  - 任一输入为非零常数时 `G = 1`。
- 重复根无需特殊处理：重数自然体现在已知因子构造中（如 `(x-1)^3`）。

## 算法：扩展子结式 PRS（抑制系数膨胀）

1. 输入 `a, b ∈ Q[x]` 先通分去分母、约去容度，化为本原整系数多项式
   `A, B ∈ Z[x]`（`a = αA, b = βB`，`α, β ∈ Q`）。
2. 在 `Z[x]` 上运行**子结式伪余式序列**（Geddes–Czapor–Labahn）：
   每步伪除后精确除以 `β_i = g·h^δ`（`g` 为上一首项系数，`h` 按
   `h ← g^δ / h^{δ-1}` 更新），使整系数位长接近理论下界，而不是经典
   欧几里得 PRS 的指数级膨胀。
3. 贝祖余因子 `u_i, v_i`（`r_i = u_i·A + v_i·B`）与余式序列满足同一
   线性递推，做同样的 `β_i` 归一化（在 `Q[x]` 中），同步抑制膨胀。
4. 末个非零余式 `r` 即 GCD（相差非零常数）：`G = monic(r)`，
   `S = u/(lc(r)·α)`，`T = v/(lc(r)·β)`，满足 `S·a + T·b = G`。

## 位长统计

`Certificate.Stats` 记录计算过程中的系数位长：

- `Steps`：伪除步数；
- `MaxRemIntBits`：子结式余式序列中整系数的最大位长；
- `MaxCofNumBits / MaxCofDenBits`：贝祖余因子分子/分母最大位长；
- `ResultNumBits / ResultDenBits`：最终证书 `(G, S, T)` 的位长。

测试中与经典（不约化）伪余式序列对比：同一输入下子结式序列最大
45 位，朴素 PRS 达 458 位。

## 证书独立验证

`Verify(a, b, cert)` 不复用任何 GCD 机制，只用独立的乘、加、除：

1. `S·a + T·b == G`（贝祖等式）；
2. `G | a` 且 `G | b`（余式为零）；
3. `G` 首一（或全零输入时 `G = S = T = 0`）。

## 测试策略

测试不用随机数值代入验等，而是**构造已知因子**：取已知公因子 `d`
（含重根，如 `(x-1)^3 (x+2)^2 (2x-3)`）与互素余因子 `p, q`，令
`a = c1·d·p, b = c2·d·q`（`c1, c2 ∈ Q`），断言 `G` 精确等于
`monic(d)`，并通过独立验证器检查贝祖证书；另有零多项式、常数、
互素、整除（一个整除另一个）、篡改证书拒绝等用例。

## 使用

```go
a := polygcd.Mul(polygcd.PolyFromInts(-1, 1), polygcd.PolyFromInts(1, 0, 1))
b := polygcd.Mul(polygcd.PolyFromInts(-1, 1), polygcd.PolyFromInts(1, 1, 1))
cert := polygcd.ExtendedGCD(a, b)   // cert.G = x-1, S*a+T*b = G
ok := polygcd.Verify(a, b, cert)    // true
```

运行测试：`go test ./...`
