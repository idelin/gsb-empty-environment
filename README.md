# CDC DDL/DML 时间线协调器

支持在线表结构变更的 CDC 处理器。当 `AlterTableAddColumn` 等 DDL 事件到达时，
协调器在数据流中插入**全局屏障（Barrier）**：排空屏障之前的旧结构 DML，
在屏障落点原子地切断并重建元数据缓存，随后恢复新结构 DML 流入，
保证屏障两侧各按其 Schema 版本解析，零字段错位。

## 设计

- `Schema`：带版本号的表结构，快照不可变（`Clone`）。
- `Coordinator`：持有元数据缓存（当前 Schema），按全局 Seq 顺序处理事件。
  - `DMLEvent`：用屏障生效时的 Schema 版本按列位解码为 `Row`；
    字段数与 Schema 宽度不一致时返回 `MisalignmentError`（拒绝静默截断/补空）。
  - `DDLEvent`：先 drain 在途旧结构 DML（计数可查），再派生新版本 Schema
    切换缓存，最后向输出流发出 `Barrier{FromVersion, ToVersion}`。
- 互斥锁保证并发读写安全（`go test -race` 验证）。

## 运行样例

```console
$ go run ./cmd/demo
=== CDC DDL/DML timeline coordinator demo ===
initial schema: &{Version:1 Columns:[id name age]}

seq=1  ROW decoded with schema v1: map[id:1 name:alice age:30]
seq=2  ROW decoded with schema v1: map[id:2 name:bob age:25]
seq=3  === GLOBAL BARRIER === schema v1 -> v2 (drain complete, metadata cache switched, resume)
seq=4  ROW decoded with schema v2: map[id:3 name:carol age:40 email:carol@example.com]
seq=5  ROW decoded with schema v2: map[id:4 name:dave age:35 email:dave@example.com]

final schema: &{Version:2 Columns:[id name age email]}
stats: 2 row(s) drained at barrier, 1 barrier(s)
```

## 测试结果

```console
$ go test -race -v ./...
=== RUN   TestTimeline_DDLSplitsOldAndNewSchema
--- PASS: TestTimeline_DDLSplitsOldAndNewSchema (0.00s)
=== RUN   TestMisalignment_NewShapeBeforeDDL
--- PASS: TestMisalignment_NewShapeBeforeDDL (0.00s)
=== RUN   TestMisalignment_OldShapeAfterDDL
--- PASS: TestMisalignment_OldShapeAfterDDL (0.00s)
=== RUN   TestMetadataCacheCut_IsImmutableSnapshot
--- PASS: TestMetadataCacheCut_IsImmutableSnapshot (0.00s)
=== RUN   TestMultipleDDLs
--- PASS: TestMultipleDDLs (0.00s)
=== RUN   TestConcurrentAccess
--- PASS: TestConcurrentAccess (0.01s)
PASS
ok      cdc-coordinator
```

验收边界覆盖：流 `DML_Old(3列) -> DDL(增加col4) -> DML_New(4列)` 中，
屏障精确落在 DDL 位点；旧行按 v1（3 列）解析、新行按 v2（4 列）解析，
`email` 列精确映射第 4 个字段；错位形状（屏障前 4 列 / 屏障后 3 列）均被拒绝。
