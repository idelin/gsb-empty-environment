# floatconv — 可配置二进制浮点格式转换内核

纯 Go 实现，核心转换**只处理位串与精确整数**（`math/big.Int`），从不经过宿主
`float32`/`float64`，因此不存在"先转到宿主浮点再舍入"的双重舍入问题。

## 格式模型

`Format` 由符号位（固定 1 位）、指数位数、尾数位数、指数偏置与特殊值规则定义：

```go
type Format struct {
    Name     string
    ExpBits  int  // 指数位数
    MantBits int  // 尾数位数（不含隐式前导 1）
    Bias     int  // 指数偏置
    HasInf   bool // 全 1 指数 + 零尾数 = 无穷
    HasNaN   bool // 全 1 指数 + 非零尾数 = NaN（尾数 MSB 为 quiet 位）
}
```

预置格式：`Binary16`（IEEE binary16）、`BFloat16`、`Binary32`、`Binary64`。
`HasInf`/`HasNaN` 均为 false 时得到纯有限格式（全 1 指数也是普通有限值），
可用于自定义 8/16/32/64 位格式。`Validate` 检查结构合法性
（总宽度为 8 的倍数且 ≤ 64、HasInf 蕴含 HasNaN 等）。

## 核心转换

`Convert(dst, src, bits, mode) (uint64, Flags, error)`：

1. 源位串解码为精确值 `(-1)^sign · num/den · 2^exp2`（`num/den` 为精确整数）；
2. 由 `floor(log2|v|)` 确定目标尺度 `f0`（正规数 `e-(p-1)`，次正规数冻结在
   `emin-(p-1)`）；
3. 用一次精确整数除法 `QuoRem(num·2^(exp2-f0), den)` 得到截断尾数与余数，
   按舍入模式决定是否进位（最近偶数用 `2·rem` 与 `den` 的精确比较判中点）；
4. 组装位串并产生标志。

四种舍入模式：`RoundNearestEven`、`RoundTowardZero`、`RoundTowardPositive`、
`RoundTowardNegative`。

标志：`invalid`、`overflow`、`underflow`、`inexact`。

- **underflow** 采用"舍入后微小且不精确"定义：结果（按无界指数范围舍入后）
  为次正规数或零 **且** 不精确时才置位。恰好舍入到最小正规数不算微小，
  不置 underflow（见 `TestUnderflowBoundary`）。
- **overflow**：舍入后指数超过 `emax` 时置位（总伴随 inexact）；最近偶数与
  同向定向模式给出 ±Inf（无 Inf 编码时饱和到最大有限值），反向定向/向零
  饱和到最大有限值。
- **正负零**保留符号。

## NaN 规则

- 符号位保留；quiet 位 = 尾数字段 MSB。
- 有效载荷**左对齐截断**：收窄时保留源载荷最高位，加宽时低位补零。
- 输出 quiet 位恒置 1：signaling NaN 转为 quiet NaN 并置 `invalid`；
  强制 quiet 位同时保证结果不会退化成无穷编码。
- 目标格式无 NaN 编码时：NaN → 同符号最大有限值，置 `invalid`。

## 十进制直接转换

`ParseDecimal(f, s, mode)` 把十进制串解析为精确整数比
`M·10^dexp = (M·5^dexp)·2^dexp`（`dexp≥0`）或 `M/(5^-dexp·2^-dexp)`（`dexp<0`），
再交给同一个整数舍入引擎。**绝不先解析为 binary64**，从构造上排除双重舍入。
支持 `inf`/`infinity`/`nan`、带符号零（`-0` → 负零）。

### 双重舍入反例（`TestDoubleRoundingCounterexample`）

取 `x = 1 + 2^-11 + 2^-70`，它是有限十进制数
`x = (2^70+2^59+1)·5^70 / 10^70`：

```
x = 1.0004882812500000000008470329472543003390683225006796419620513916015625
```

- **直接转 binary16（最近偶数）**：候选为 `1`（0x3C00）与 `1+2^-10`（0x3C01），
  中点 `m = 1+2^-11`。`x = m + 2^-70 > m`，故**向上**舍入为 `0x3C01`。
- **先转 binary64**：binary64 精度 p=53，在 `1+2^-11` 附近间距为 `2^-52`，
  且 `m` 本身可精确表示。`x` 到 `m` 的距离为 `2^-70`，到 `m+2^-52` 的距离为
  `2^-52 - 2^-70 > 2^-70`，故 `float64(x) = m`。再把 `m` 收窄到 binary16 时
  恰好是中点，ties-to-even 选尾数为偶的 `0x3C00`。
- 结果：直接转换 `0x3C01` ≠ 双重舍入 `0x3C00`。测试同时断言
  `math.Float64bits(f64) == 0x3FF0020000000000`（即 `1+2^-11`）以固定推导链。

## 批量转换

`ConvertBatch(dst, dstFmt, src, srcFmt, mode) (Flags, error)`，元素按小端存储，
返回所有元素标志的按位或。

- **部分重叠**：`dst` 与 `src` 可任意重叠（含非元素对齐偏移），语义等价于
  先完整读出源序列再转换（实现上检测到重叠时先快照源缓冲区）。
- **结构错误原子性**：格式非法、源长度不是元素大小整数倍、目标缓冲区不足
  等错误在任何写入之前检测，目标缓冲区保持原样。

## 参考实现

`ref/` 是独立的参考实现：全部用 `math/big.Rat` 构造有理数并做有理数比较
（中点判定用 `Rat.Cmp(1/2)`），与生产的整数舍入引擎是**两条独立代码路径**，
测试中对二者做位级 + 标志级对拍。

## 验证

```
$ go test ./...
ok      floatconv       2.250s
?       floatconv/ref   [no test files]
```

覆盖：

- `TestExhaustiveBinary16ToBinary32VsRef` / `TestExhaustiveBFloat16ToBinary32VsRef`：
  全部 65536 个位型 × 4 种舍入模式，与有理数参考实现对拍。
- `TestRoundTripBinary16ThroughBinary32` / `TestRoundTripBFloat16ThroughBinary32`：
  穷举全部 16 位位型，有限值与带符号零"扩展再收缩"位相等；
  NaN 按声明的规范化（quiet 位置 1、载荷左对齐保留、符号保留）核验；
  sNaN 首次转换置 `invalid`。
- `TestMidpoints`：对全部相邻 binary16 有限值对（含次正规区间与 2 的幂边界）
  精确构造中点及中点 ±2^-40 微移值，核验 4 种舍入模式；抽样负值对。
- `TestOverflowBoundary` / `TestUnderflowBoundary`：最大有限值与 2^16 之间的
  中点及邻值、零与最小次正规数的中点、最大次正规数与最小正规数的中点，
  逐一核验结果位型与 overflow/underflow/inexact 标志。
- `TestBinary64ToBinary32VsHardware`：50 万随机 binary64 位型转 binary32
  最近偶数，与 CPU 的 `float32(float64)` 结果逐位一致（NaN 载荷除外）。
- `TestDoubleRoundingCounterexample`：上述十进制反例。
- `TestParseDecimalVsRef`：随机十进制串 × 3 格式 × 4 模式与参考实现对拍。
- `TestBatchOverlap` / `TestBatchErrorsLeaveDstUntouched`：重叠等价性与
  错误时目标缓冲区不变性。

## 吞吐基准（可复现）

```
$ go test -run '^$' -bench=. -benchmem -benchtime=1s .
goos: darwin
goarch: arm64
pkg: floatconv
cpu: Apple M1 Pro
BenchmarkConvertBatchBinary32ToBinary16-10     68    16037633 ns/op    16.35 MB/s   14040644 B/op   450300 allocs/op
BenchmarkConvertBatchBinary16ToBinary32-10    366     4293828 ns/op    30.53 MB/s    1496968 B/op    52550 allocs/op
BenchmarkParseDecimalBinary16-10           1000000       1251 ns/op                     640 B/op       17 allocs/op
```

每批 65536 个元素；binary32→binary16 约 245 ns/元素。吞吐的代价来自
"每元素精确整数运算"的设计选择——它换取了对任意可配置格式的位级正确性
与零双重舍入保证。
