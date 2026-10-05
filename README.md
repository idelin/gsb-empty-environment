# binom

组合数查询库：返回精确二项式系数（`big.Int`），以及模**任意正整数**的值——
合数模数下不假定阶乘可逆。

## 原理

- `Exact(n, k)`：乘法递推精确计算 C(n, k)。
- `Calculator.Mod(n, k, m)`：把 m 分解为素幂 p^a，分别计算
  C(n, k) mod p^a（用 Legendre/Kummer 求 p 进赋值，阶乘先剥去所有
  p 因子再取逆，因此不需要阶乘在模 m 下可逆），最后 CRT 合并。
- `Factorizer`：试除 + Miller-Rabin + Pollard rho，结果按模数缓存，
  批量查询（`ModBatch`）共享缓存；`budget` 限制整个生命周期内的
  分解工作量（试除与 rho 迭代次数），耗尽返回 `ErrBudgetExceeded`。
  素性判定与缓存命中不消耗预算。

## 边界行为

- `k < 0` 或 `k > n`：结果为 0，且不消耗分解预算。
- `n = 0`：C(0, 0) = 1。
- `m = 1`：结果为 0；`m = 0`：返回 `ErrNonPositiveModulus`。

## 用法

```go
exact := binom.Exact(100, 50)          // *big.Int

calc := binom.NewCalculator(1 << 20)   // 分解预算
r, err := calc.Mod(100, 50, 1<<30)     // C(100,50) mod 2^30
rs, err := calc.ModBatch([]binom.Query{{N: 100, K: 50, M: 360}, ...})
```

## 测试

```sh
go test ./...
```

覆盖：k 越界、n=0、模数 1、高次素因子模数（2^63、2^20·3^12 等）、
半素数分解、预算耗尽、批量共享分解缓存，并与大整数精确结果交叉验证。
