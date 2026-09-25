# kvimport — 流式键值导入器（崩溃可恢复）

纯 Go 标准库实现。把 JSON 行记录流式导入调用方指定的目录，带检查点、
坏记录隔离、原子发布；故障通过注入点模拟。

## 输入与产物

输入：每行一个 JSON 对象 `{"k":"key","v":"value"}`。

`Options.Dir` 内的文件布局：

| 文件 | 作用 |
|---|---|
| `data.log` | 暂存区：已确认批次的记录日志（非对外输出） |
| `rejects.log` | 隔离区：坏记录（偏移、原因、原文），导入不中断 |
| `checkpoint.json` | 检查点：输入摘要 + 最后确认偏移 + 暂存区已确认大小 |
| `store.json` | 最终发布的快照，只通过原子 rename 出现 |

**最终发布前不可见未完成批次**：所有中间状态只落在暂存文件里；
`store.json` 在全部输入消费完后，经「写临时文件 → fsync → rename →
目录 fsync」一次性原子出现。读者只能看到旧快照或新快照，永远看不到半个批次。

## 检查点与提交流程

每攒满 `BatchSize` 条好记录（以及 EOF 时）提交一个批次：

1. 把批次追加到 `data.log` / `rejects.log`；
2. 两个文件 `fsync`；
3. 原子替换 `checkpoint.json`（临时文件 + rename + 目录 fsync）。

检查点内容（`checkpoint.json`）：

```json
{"schema":1,"offset":136,"digest":"64d8...5c","dataSize":140,"rejectsSize":186,"sum":"788a...45"}
```

- `offset`：最后确认偏移（已确认输入字节数，按行边界对齐）；
- `digest`：`input[0:offset]` 的 SHA-256，即输入摘要；
- `dataSize` / `rejectsSize`：暂存文件中属于已确认批次的前缀长度；
- `sum`：对上述字段的校验和，用于识别撕裂/损坏的检查点。

检查点是唯一的提交点：它落盘之前的批次一律视为未确认。

## 崩溃恢复协议

重启后再次调用 `Import`（同一目录、同一输入）：

1. 无检查点 → 视为全新导入，丢弃任何暂存残留（崩在第一个检查点之前）。
2. 有检查点 → 重读输入前 `offset` 字节并重算 SHA-256：
   **与摘要不一致（含输入变短）即拒绝恢复**，报 `input changed`。
3. 把 `data.log` / `rejects.log` 截断到检查点记录的已确认大小——
   未确认尾部（含崩在追加中途造成的半行）整体丢弃。
4. 从已确认的 `data.log` 前缀重建内存状态（键 → 值、版本）。
5. 从 `offset` 处继续流式读取。

**重放最后一块不造成重复键版本增长**：版本不是独立计数器，而是从
已确认日志重建/推导出来的。未确认的尾部在恢复时被截掉，重放同一批
记录只是把同样的日志再写一遍，版本与无故障运行完全一致
（测试 `TestCrashBeforeCheckpointReplayHasNoVersionGrowth` 对比了
崩溃恢复与干净运行的 `store.json` 字节级相等）。

**末行截断不能提交半条**：没有换行符结尾的尾部字节永远不会被解析成
记录；它们被计入偏移/摘要并作为 `truncated record` 写入隔离区，
恢复时不会重复处理。

**坏记录隔离**：非法 JSON、缺字段、空键、未知字段、行尾垃圾等逐条
写入 `rejects.log`（含偏移、原因、原文），导入继续；空行静默跳过。

## 断电保证的边界

保证成立的假设：文件系统遵守 fsync 语义（fsync 返回后数据持久）、
rename 在同一文件系统内原子、存储设备不谎报刷盘。

- **已确认批次**：崩进程、断电都不丢。顺序是「暂存 fsync → 检查点
  原子替换」，检查点存在则其覆盖的暂存前缀必然已持久。
- **未确认批次**（当前正在攒的批次）：允许丢失，恢复时重放，幂等。
- **检查点撕裂写**：由 `sum` 校验和识别并拒绝（宁可报错也不静默
  用坏状态恢复）；正常路径靠 rename 原子性避免撕裂。
- **rename 与目录 fsync 之间断电**：rename 可能生效也可能不生效，
  两种结果都是一致状态（旧检查点/旧快照 或 新检查点/新快照），
  恢复后都能继续或重放。
- **输入变更检测的范围**：只覆盖已确认前缀 `[0, offset)`。最后确认
  偏移之后发生的输入变化无法与「正常的未读后续」区分——这是流式
  摘要的固有边界。若需要全文件级保证，调用方应在导入前自行固定输入
  （如先整体算好摘要并在恢复时比对）。
- **恢复开销**：恢复时需重读已确认前缀来验证摘要，复杂度
  O（已确认字节数）。
- **单写者假设**：同一目录同一时刻只允许一个导入进程；不实现
  跨进程锁（崩溃会留下陈旧锁文件，交由部署侧保证）。
- **不防**：文件被外部篡改、文件系统/硬件不遵守 fsync 语义、
  磁盘整盘损坏。

## 故障注入点

`Options.Inject func(point string, batch int) error` 在以下命名点被
调用，返回错误即模拟该点崩溃：

- `afterDataSync`：批次已追加并 fsync，检查点未写（→ 重放路径）；
- `afterCheckpoint`：检查点已落盘（→ 不重放）；
- `beforePublish`：输入已消费完，快照未发布（→ 无 `store.json`）；
- `afterPublishRename`：快照已 rename，目录未 fsync（→ 快照已完整可见）。

## 运行样例

```console
$ go run ./cmd/kvimport -dir /tmp/kvdemo -in examples/sample.jsonl -batch 2
import ok (resumed=false): records=4 rejects=2 keys=3 offset=136
digest=64d8cd3db32fe69ee3871fb671af014041d7a63b58984009b356b48ec1c6d15c
published=/tmp/kvdemo/store.json

$ cat /tmp/kvdemo/rejects.log
{"off":69,"reason":"invalid JSON: ...","raw":"this is not json"}
{"off":111,"reason":"missing k or v field","raw":"{\"k\":\"cherry\"}"}

# 崩在第 2 个批次的 data sync 之后、检查点之前：
$ go run ./cmd/kvimport -dir /tmp/kvdemo2 -in examples/big.jsonl -batch 2 \
    -fail-at afterDataSync -fail-after 2
import failed: kvimport: injected fault at afterDataSync (batch 2): simulated crash
$ ls /tmp/kvdemo2          # 没有 store.json：未完成批次不可见
checkpoint.json  data.log  rejects.log

# 原样重跑即恢复；k1 的 version 是 3 而不是被重放撑大：
$ go run ./cmd/kvimport -dir /tmp/kvdemo2 -in examples/big.jsonl -batch 2
import ok (resumed=true): records=6 rejects=0 keys=4 offset=114

# 输入变了则拒绝恢复：
$ go run ./cmd/kvimport -dir /tmp/kvdemo2 -in changed.jsonl -batch 2
import failed: kvimport: input changed: digest mismatch over first 114 bytes ...
```

## 测试

```console
$ go test ./...
ok  	kvimport
```

覆盖：正常导入与版本计数、坏记录隔离、末行截断、检查点前崩溃重放
（版本不增长、与干净运行字节级一致）、检查点后崩溃、输入变更拒绝、
发布前崩溃不可见、检查点腐化拒绝、隔离区重放不重复、完成后重跑幂等、
空输入、publish rename 后崩溃。
