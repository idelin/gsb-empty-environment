# evidence-verifier

证据包（trace / log / metric）验真器：校验版本化清单、摘要与跨文件引用，报告缺件与时间范围不一致。纯 Go 标准库，绝不修改输入证据。

## 设计要点

- **增量到达**：证据以 `Chunk{Version, Name, Data}` 逐块签收，按版本隔离暂存（`staging/<version>/`），任意到达顺序均可。
- **原子签收**：`Commit(manifest)` 要么全部校验通过并封存（temp+rename 原子落盘），要么整体拒绝、暂存保留，迟到块到达后可重试。旧版本的块不能拼接进新清单（按版本隔离 + 封存后拒收）。
- **分类报告**：`missing_file`、`bad_digest`、`unknown_version`、`name_conflict`、`dangling_reference`、`reference_unverified`、`time_range_mismatch`、`unexpected_file`、`parse_error` 各自独立；空文件（已到达、0 字节、空摘要匹配）与缺文件（块未到达）明确区分。
- **跨文件引用**：只有目标文件在该版本下通过摘要/大小/时间范围校验后引用才成立，否则报 `reference_unverified`；引用不存在的日志记录报 `dangling_reference` 并定位到具体源记录（文件 + 记录 ID）。
- **恢复等价**：所有状态持久化在验真器自有目录，重启后扫描恢复；任意到达顺序 + 中途崩溃恢复与一次全量验证结果一致（测试断言）。
- **本地遥测**：事件（`chunk_staged`/`commit_sealed`/...）在本地合成，可写入内存或 JSONL 文件；`Replay` 可用夹具（`verifier/testdata/fixture_v1.json`）回放完整会话。不接监控平台，无展示看板。

## 运行样例

```console
$ go run ./cmd/evverify -tamper none     # none|reference|digest|missing|timerange|empty
== staging 3 chunk(s) for version v1 ==
== committing manifest v1 (digest 67198f3a4082) ==
{
  "version": "v1",
  "status": "sealed",
  "issues": null,
  "files": [
    {"name": "trace.jsonl",   "kind": "trace",  "records": 2, "empty": false, ...},
    {"name": "app.log",       "kind": "log",    "records": 2, "empty": false, ...},
    {"name": "metrics.jsonl", "kind": "metric", "records": 2, "empty": false, ...}
  ]
}
== telemetry (locally synthesized) ==
#01 recovered        ...
#02 chunk_staged     v1  trace.jsonl    staged 222 bytes, sha256=93481a0e...
#05 commit_started   v1  manifest digest 67198f3a..., 3 file(s), 2 reference(s)
#06 commit_sealed    v1  all checks passed; version sealed atomically
```

篡改单条引用（`span-1` 指向不存在的 `log-999`）：

```console
$ go run ./cmd/evverify -tamper reference
"status": "rejected",
"issues": [{
  "category": "dangling_reference",
  "file": "trace.jsonl", "record": "span-1",
  "targetFile": "app.log", "targetRecord": "log-999",
  "detail": "record \"span-1\" in trace.jsonl cites record \"log-999\" in app.log, which does not exist"
}]
```

空文件与缺文件区分（`notes.log` 空文件验证通过，`ghost.log` 报缺件）：

```console
$ go run ./cmd/evverify -tamper empty
"issues": [{"category": "missing_file", "file": "ghost.log",
  "detail": "declared as an empty file but even the empty chunk never arrived"}]
```

## 测试结果

```console
$ go test ./... -count=1
?   	evidence-verifier/cmd/evverify	[no test files]
ok  	evidence-verifier/verifier	0.499s
```

覆盖：干净包封存、缺件与原子重试、空文件 vs 缺文件、坏摘要阻断引用、篡改引用定位源记录、时间范围不一致、同名不同摘要拒绝、未知版本与幂等提交、旧块不可拼接、多余块拒封、任意到达顺序等价（全排列）、崩溃恢复等价、输入不可变、本地遥测序列、夹具回放。

## 布局

- `verifier/types.go` — 清单、记录、问题分类与报告模型
- `verifier/verifier.go` — 签收、原子提交、引用与时间范围校验
- `verifier/store.go` — 持久化与崩溃恢复（temp+rename）
- `verifier/telemetry.go` — 本地遥测合成（内存 / JSONL 文件）
- `verifier/fixture.go` — 动作夹具与回放（可模拟中途重启）
- `verifier/sample.go` — 确定性样例证据包合成器（含篡改模式）
- `cmd/evverify` — 运行样例 CLI
