# bitlayout — 位级记录布局编译器

纯 Go 标准库实现。输入一份 JSON schema，编译产出**可复用、可并发共享**的编码计划与增量解码计划（`Plan`）。

## 支持的子集（除此之外一律编译期拒绝）

| kind | 说明 |
|---|---|
| `int` | 定宽整数，1–64 位，`signed` 选择补码/无符号 |
| `array` | 长度由**先前兄弟字段** `lengthFrom` 决定的数组 |
| `union` | 由**先前兄弟字段** `tagFrom` 选择分支的联合 |
| `pad` | 对齐填充（保留位，解码时必须为 0） |
| `checksum` | 覆盖指定已编码区间 `[from,to]`（顶层字段名，含端点）的校验字段，算法 `sum` / `xor` / `crc8` |
| `struct` | 命名字段组（可嵌套，数组元素可以是 struct，从而表达嵌套数组） |

## 位序与字节序是两个独立属性

一个 n 位字段先按 **字节序** 切成 8 位组（最高组不足 8 位）：`big` 先发最高组，`little` 先发最低组。每个组内再按 **位序** 逐位发出：`msb` 先发组内最高位（流位 i → 字节 `i/8` 的位 `7-i%8`），`lsb` 先发组内最低位（流位 i → 字节 `i/8` 的位 `i%8`）。校验和的输入为被覆盖流位按**流自身位序**打包的字节序列（末尾补零）。

## 编译期拒绝（`Compile` 返回错误）

- **向前长度引用**：`lengthFrom` / `tagFrom` 必须指向同一字段列表中**更早**的兄弟；指向自身或后续字段即拒绝。
- **循环依赖**：编译后对 length/tag/checksum 依赖图做 DFS 三色检查（向后引用规则已结构性杜绝环，此检查为兜底）。
- **字段覆盖**：同一作用域内重名字段拒绝；顺序布局从结构上保证位区间不相交。
- **未定义标签**：union 的 case 键必须是十进制数且落在 tag 字段位宽内；`tagFrom` 必须存在且为无符号 int。
- **无界分配**：数组必须有 `lengthFrom`；由长度字段位宽推出的单数组最坏元素数超过上限（默认 2^20，可用 `CompileOptions.MaxElements` 调整）即拒绝；嵌套最坏规模用 64 位溢出检查乘法推导。

## 编码：先验证，后原子写入

`plan.Encode(rec, buf)` 先走完整的 `Validate`（类型、值域、数组长度与长度字段一致、union tag 有定义、总位数、目标容量），**全部通过后才写入调用方缓冲区**；任一失败 `buf` 逐字节不变（测试用 0xAA 哨兵验证：最后一个元素越界时缓冲区不变）。写入前会把记录区间 `[0,nbits)` 清零，因此保留位恒为 0；记录区间之外的位不触碰。校验和在写入过程中从已写位回填计算。

## 解码：可停在任意一位，失败不交付半个记录

- 一次性：`plan.Decode(buf, nbits, opts)`。
- 增量：`s := plan.NewSession(opts)`；`s.Feed(chunk, nbits)` 支持**任意位位置**切块；`s.Try()` 返回 `(nil,false,nil)` 表示缺输入。会话持有独立输入缓冲、游标与额度，互不影响。
- 错误分类（`*bitlayout.Error`，含 schema 路径与输入位偏移）：
  - `ErrNeedMoreInput` 缺输入（所有真前缀都必然得到它，测试逐位验证）；
  - `ErrReservedBits` 非法保留位（pad 非零）；
  - `ErrValue` 非法值（值越界、未定义 tag、校验和不匹配）；
  - `ErrQuota` / `ErrCapacity` / `ErrOverflow`。
- 任何失败返回的 record 为 `nil`——后置校验失败的记录不会改变输出对象（测试验证一次性与会话两种路径）。

## 额度（每会话隔离）

`DecodeOptions{MaxDepth, MaxTotalElements, MaxTotalBits}`：嵌套深度、全记录元素总数（溢出安全累加）、记录总位数。长度乘法在编译期（最坏规模）与运行期（实际累加）都做 64 位溢出检查。

## 执行开销（按 schema 规模与实际记录大小推导）

设 `F` = schema 节点数，`B` = 实际记录位数，`C` = 校验覆盖位数，`E` = 实际元素总数：

| 操作 | 时间 | 空间 |
|---|---|---|
| 编译 | `O(S)`，S = schema JSON 大小 | `O(F)` |
| 验证 | `O(F' + E)`（F' 为实际走到的字段） | `O(1)` 额外 |
| 编码 | 验证 + `O(B/8 + C/8)` 字节级写 | `O(1)` 额外（直写调用方缓冲） |
| 解码 | `O(F' + B + C/8)`，每位读一次、校验区间再读一次 | `O(B/8 + E)`（会话输入缓冲 + 值树） |
| 增量 Try | 每次 `Try` 为一次完整解码尝试；k 次切块总成本 `O(k·B)` | 会话间完全隔离 |

`plan.Cost()` 在编译期给出静态上界（`MinRecordBits` / `MaxRecordBits` / `MaxElements` / `ChecksumBits` / `MaxDepth`）。

实测（Apple M1 Pro，sensor 样例：12 个 schema 节点、64 位记录、53 位校验覆盖）：

```
BenchmarkValidateSensor-10   1000000   125.4 ns/op
BenchmarkEncodeSensor-10     1000000   602.0 ns/op   (含完整 Validate)
BenchmarkDecodeSensor-10     1000000   693.6 ns/op
BenchmarkCompileSensor-10    1000000  13421  ns/op
```

与模型一致：解码 ≈ 64 位读 + 53 位校验重读 + 值树分配；编码 ≈ 验证(125ns) + 64 位写 + 校验回填。

## 验证方法

- **独立逐位参考实现** `internal/refimpl`：自行解析 schema JSON、逐位处理，与生产计划交叉验证。
- **穷举小位宽记录**：13 位 tag+value 全部 8192 种输入；12 位 union+pad+checksum 全部 4096 种；跨字节数组（lsb/little）16 位全部 65536 种 + 24/32 位各 2 万采样；小 schema 全部合法记录的编码位流逐一与参考实现比对。
- **所有切块方式**：64 位验收记录的全部 65 种二切分与全部 2080 种三切分；所有真前缀必须报缺输入。
- **原子性**：末元素越界 / 容量不足 → 目标缓冲区逐字节不变；校验失败 → 不交付记录。
- **竞态**：`go test -race` 下 8 goroutine × 200 轮共享同一 Plan 混合编码/解码/逐字节会话——**无数据竞争**（本次运行结果：`ok bitlayout 9.914s`，23 个测试全部通过）。

## 运行样例

```console
$ go run ./cmd/bitlayout compile examples/sensor.json
{ "name": "sensor", "minRecordBits": 25, "maxRecordBits": 212, ... }

$ go run ./cmd/bitlayout encode examples/sensor.json examples/sensor.record.json
64 bits: 36a1d5e091fff857

$ go run ./cmd/bitlayout decode examples/sensor.json 36a1d5e091fff857 64
consumed 64 bits:
{ "tag": 1, "level": -300, "body": { "count": 3, "samples": [2748, 291, 4095] }, "crc": 87 }
```

样例：`examples/sensor.json`（3 位标签 + 10 位有符号值 + 两种长度联合分支 + 跨字节 12 位数组 + 尾部 CRC-8，msb/big），`examples/nested.json`（嵌套数组，lsb/little）。

## 测试

```console
$ go test ./...        # 全部功能测试
$ go test -race .      # 竞态检测
$ go test -bench . .   # 基准
```
