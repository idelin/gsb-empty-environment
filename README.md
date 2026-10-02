# chrono — 严格分离的时间类型系统（Go 标准库）

四种时间概念分别建模，**禁止隐式互转**：

| 类型 | 含义 | 元数据 |
|---|---|---|
| `Instant` | 物理时间点（Unix 纪元起纳秒） | `Precision` |
| `WallClock` | 墙钟读数（民用字段 + 选中偏移） | `Precision`、时区表版本、`OffsetChoice` |
| `Duration` | 物理时长（纳秒，算术全检查） | — |
| `OffsetChoice` | 重叠墙钟的偏移选择（earlier/later） | — |

`WallClock → Instant` 的唯一路径是显式的 `Resolve(w, reg)`：需要按元数据中的
`zone@version` 查表，重叠读数必须带 `OffsetChoice`，否则拒绝。墙钟值永远不会被
静默当作瞬时时间（迁移函数只产出 `WallClock`，不产出 `Instant`）。

## 演进与迁移证据

- **精度缩放** `ScaleInstant` / `ScaleWallClock`：向上无损可逆；向下截断记录
  `Loss{precision-truncation, dropped N ns}`。反向用 `ReverseScaleInterval`
  返回全部候选的**精度区间** `[v, v+unit-1]`。纳秒直接缩到目标网格，
  不经毫秒中间截断。
- **时区表更新** `MigrateWallClock`：民用字段与 `OffsetChoice` 原样携带，
  偏移按新表重算。仍重叠→重放选择（可逆）；变唯一→记录
  `Loss{ambiguity-collapsed}`，选择作为元数据保留以便回程重放；变空洞→
  迁移失败，不发布对象。
- **反向迁移** `ReverseWallClock`：返回旧表下的**全部候选**（每个候选带各自
  的 `OffsetChoice`），调用方按保留的选择元数据挑回。
- 每次迁移返回 `Migration{Reversible, Losses, Notes}` 作为可逆/有损证据。

## 不变式

- 整数溢出、越界（自定义序列化范围 `[-2^62, 2^62-1]` ns）、非法字段/标签
  在构造函数与解码器里被拒绝，**对象发布前**拦截。
- 解码是纯数据校验：不执行宿主代码；长度先与剩余输入比对再分配
  （`decoder.take` / `decoder.str`，字符串上限 256）。
- 未知时区表版本 → `ErrUnknownVersion`，标为不可解析，绝不猜测。

## 合成多版本回拨表

`LabZones()` 提供 `Lab/Zone` 的 v1/v2/v3：v1 在 2024-10-27 回拨（重叠），
v2 把回拨推迟一周（该墙钟变唯一），v3 废除夏令时。测试用它核验
v1→v2→v3→v2→v1 往返恢复原值，且中间各步的损耗证据符合预期。

## 运行样例（`go run ./cmd/demo`）

```
== overlap: one wall reading, two instants ==
earlier choice -> Instant{1729989000000000000 ns, prec=ns}
later   choice -> Instant{1729992600000000000 ns, prec=ns}

== multi-rollback migration v1 -> v2 -> v3 -> v1 ==
start: WallClock{2024-10-27 02:30:00.000000000 +3600s later Lab/Zone@v1 prec=ns}
migrate wallclock(Lab/Zone@v1) -> wallclock(Lab/Zone@v2): reversible=false
  loss[ambiguity-collapsed]: new table has a single candidate; recorded choice retained as metadata only
  note: offset changed +3600s -> +7200s
migrate wallclock(Lab/Zone@v2) -> wallclock(Lab/Zone@v3): reversible=false
  loss[ambiguity-collapsed]: new table has a single candidate; recorded choice retained as metadata only
  note: offset changed +7200s -> +3600s
migrate wallclock(Lab/Zone@v3) -> wallclock(Lab/Zone@v2): reversible=false
  loss[ambiguity-collapsed]: new table has a single candidate; recorded choice retained as metadata only
  note: offset changed +3600s -> +7200s
migrate wallclock(Lab/Zone@v2) -> wallclock(Lab/Zone@v1): reversible=true
  note: ambiguity preserved under new table; recorded choice re-applied
  note: offset changed +7200s -> +3600s
after round trip: WallClock{2024-10-27 02:30:00.000000000 +3600s later Lab/Zone@v1 prec=ns}
round trip restored original: true

== reverse migration returns all candidates ==
migrate wallclock(Lab/Zone@v1) -> wallclock(Lab/Zone@v1): reversible=true
  note: 2 candidate(s) under v1
  candidate: WallClock{2024-10-27 02:30:00.000000000 +7200s earlier Lab/Zone@v1 prec=ns}
  candidate: WallClock{2024-10-27 02:30:00.000000000 +3600s later Lab/Zone@v1 prec=ns}

== precision scaling with evidence ==
original: Instant{123456789 ns, prec=ns}
migrate instant(prec=ns) -> instant(prec=us): reversible=false
  loss[precision-truncation]: dropped 789 ns below the us grid -> Instant{123456000 ns, prec=us}
migrate instant(prec=ns) -> instant(prec=ms): reversible=false
  loss[precision-truncation]: dropped 456789 ns below the ms grid -> Instant{123000000 ns, prec=ms}
reverse interval for Instant{123000000 ns, prec=ms}: [123000000, 123999999] ns

== codec round trip ==
01 03 00 00 00 00 07 5b cd 15 -> Instant{123456789 ns, prec=ns}
03 03 00 00 07 e8 0a 1b 02 1e 00 00 00 00 00 00 00 0e 10 02 00 08 4c 61 62 2f 5a 6f 6e 65 00 02 76 31 -> WallClock{2024-10-27 02:30:00.000000000 +3600s later Lab/Zone@v1 prec=ns}

== unknown tz version is unresolvable ==
resolve v99: chrono: unknown timezone table version: Lab/Zone@v99
```

## 测试结果（`go test -v ./chrono/`）

```
--- PASS: TestOverlapPreservesSelectedOffset (0.00s)
--- PASS: TestAmbiguousAndGapRejected (0.00s)
--- PASS: TestUnknownTZVersionUnresolvable (0.00s)
--- PASS: TestPrecisionScalingEvidence (0.00s)
--- PASS: TestNanosNotTruncatedThroughMillis (0.00s)
--- PASS: TestMultiRollbackRoundTrip (0.00s)
--- PASS: TestReverseMigrationReturnsAllCandidates (0.00s)
--- PASS: TestOverflowRejected (0.00s)
--- PASS: TestIllegalFieldsRejected (0.00s)   (10 个子测试全部通过)
--- PASS: TestCodecRoundTrip (0.00s)
--- PASS: TestCodecRejectsBadInput (0.00s)
--- PASS: TestSerializationRangeEdges (0.00s)
PASS
ok  	chrono/chrono
```

## 布局

- `chrono/types.go` — 四种类型、校验构造函数、自定义序列化范围
- `chrono/tz.go` — 版本化时区表、候选计算、`Resolve`
- `chrono/migrate.go` — 精度缩放、时区迁移、可逆/有损证据、反向候选
- `chrono/codec.go` — 二进制编解码（先验长度再分配，非法标签拒绝）
- `chrono/lab.go` — 合成多版本回拨表 `Lab/Zone` v1/v2/v3
- `chrono/chrono_test.go` — 全部测试
- `cmd/demo/main.go` — 运行样例
