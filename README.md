# Decimal128 定点内核

面向向量化计算引擎的 SQL `DECIMAL(p, s)`（`1 <= p <= 38`）定点运算内核，
纯 Go 实现，可直接导入。核心定长整数运算全部基于 `uint64` limb 手工实现，
**生产代码不导入 `math/big`**；`math/big`（`big.Int`/`big.Rat`）仅出现在
`*_test.go` 中作为精确有理数参考对照。

## 内部表示

- `decimal128/i128.go`：自定义定长整数
  - `i128`：有符号 128 位补码，高 64 位 `int64` + 低 64 位 `uint64`
  - `u128` / `u256`：无符号 128 / 256 位，schoolbook 乘法与二进制长除法
  - 乘法内部始终保留完整 256 位宽积（8-limb、512 位累加器），绝不提前截断
- `decimal128/decimal.go`：`Decimal128{coeff i128, scale, precision}`，
  表示 `coeff * 10^-scale`，取值范围 `[-10^38+1, 10^38-1]`
- `decimal128/tables.go`：`10^k` 幂表（u128 到 38 位，u256 到 77 位）

## SQL 结果类型推导

`decimal128/arithmetic.go` 实现主流 SQL（SQL Server 同族）规则并钳制到 38 位：

| 运算 | 精度 p | 尺度 s |
| --- | --- | --- |
| 加减 | `p1-s1+s2+max(6, s1-p2+1)` | `max(6, s1-p2+1)` |
| 乘法 | `p1+p2+1` | `s1+s2` |
| 除法 | `p1-s1+s2+max(6, s1+p2+1)` | `max(6, s1+p2+1)` |

当推导出的 `p > 38` 时，按超出量缩减尺度（尺度最低到 0）；若缩减后仍超界，
由系数级 38 位边界检查统一返回 `ErrOverflow`。

## 运算与舍入

- 加减：无损对齐到最大 scale，再按结果尺度缩减
- 乘法：256 位宽积 → 按尺度缩减并做 38 位溢出检测
- 除法：分子扩到 256 位并乘 `10^k`（`k<0` 时并入除数，保证余数精确），
  无符号长除法 + 精确余数判定半点
- 取模：商向零截断，余数符号与被除数一致；除零返回 `ErrDivisionByZero`
- 两种舍入模式
  - `HalfAwayFromZero`：四舍五入远离零
  - `HalfToEven`：银行家偶舍入（半点看商的奇偶）
- 字符串：`Parse` 支持科学计数法、前导零、负零归一化；`String` 按 scale
  补齐尾部零，`StringScientific` 输出规范化科学计数法

## 用法

```go
import "prescreen.local/ps-41ac0318f0/decimal128"

a, _ := decimal128.Parse("99999999999999999999.99")
b, _ := decimal128.Parse("0.001")

ctx := decimal128.Context{Mode: decimal128.HalfToEven}
q, err := ctx.Div(a, b)
r, err := ctx.Mod(a, b)
_ = q.String()
_ = r.String()
```

## 验收

```sh
go vet ./...
go test -count=1 ./...          # big.Int/big.Rat 精确对照
go test -fuzz FuzzArithmetic -fuzztime 30s ./decimal128
go test -fuzz FuzzParse -fuzztime 30s ./decimal128
```

测试覆盖：38 位极值乘除不溢出、跨 64 位进位/借位、负数取模与向零截断
一致性、两种半值舍入在正负数上的差异、超 `10^38-1` 溢出捕获、定长
128/256 位乘除法对 `math/big` 的随机对照。
