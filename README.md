# evidence — 证据包验真器

用 Go 标准库实现的 trace / 日志 / 指标证据包验真器。支持增量到达、
版本化清单与原子签收；只读输入，绝不修改原始证据。

## 结构

- `manifest.go` — 版本化清单与文件条目（名称、SHA-256、大小、类别、声明时间范围）
- `records.go` — 记录解析（`id|RFC3339Nano|payload`，payload 内 `ref=<file>:<record>` 为跨文件引用）
- `store.go` — 块缓冲与原子签收存储；按 `(version, name)` 键控，可落盘恢复
- `verifier.go` — 验真器：清单接收、块接收、全量验证、分类报告
- `telemetry.go` — 本地合成的遥测事件，可序列化为 JSON 夹具并回放
- `cmd/evidence-demo` — 运行样例

## 需求映射

- **缺件 / 坏摘要 / 未知版本分别报告**：`MISSING_FILE`、`BAD_DIGEST`、
  `UNKNOWN_VERSION` 为独立 finding 代码（另有 `EMPTY_FILE`、`UNKNOWN_FILE`、
  `TIME_RANGE_MISMATCH`、`DANGLING_REFERENCE`、`NAME_DIGEST_CONFLICT`）。
- **空文件与缺文件区分**：声明大小为 0 且摘要等于空内容摘要时报告
  `EMPTY_FILE`（信息性，不判失败）；声明有内容但无签收内容才是 `MISSING_FILE`。
- **原子签收，旧块不能拼接新清单**：块按到达时的清单版本键控；只有收齐
  声明大小且摘要匹配该版本钉住的条目时才原子提交（内存提交 + 落盘
  tmp+rename）。旧版本密封内容对新版本清单不可见。
- **同文件名不同摘要拒绝**：同一清单内同名条目摘要冲突时，清单整体被拒
  并报告 `NAME_DIGEST_CONFLICT`。
- **跨文件引用在目标版本校验后成立**：引用只在源文件与目标文件均通过
  摘要校验后才求值；目标未验证时只报文件级 finding。
- **篡改单一引用定位具体源记录**：`DANGLING_REFERENCE` 携带源文件与源
  记录 ID（如 `trace.jsonl#T9`）。
- **时间范围不一致**：记录时间戳超出清单声明的 `[Start, End]` 时报告
  `TIME_RANGE_MISMATCH`，定位到具体记录。
- **任意到达顺序及恢复与一次全量验证一致**：报告按 (file, record, code)
  排序，与到达顺序无关；密封文件可落盘，重启后恢复验证结果与一次性
  全量验证完全一致（`TestArrivalOrderEquivalence`、`TestRecoveryEquivalence`）。
- **绝不修改输入**：块到达即拷贝，密封内容写入独立存储目录；
  `TestInputsNeverModified` 校验源目录哈希不变且无新增文件。
- **遥测本地合成、夹具回放**：事件仅记录在内存，可 `SaveFixture` /
  `ReplayFixture`；不接监控平台，无展示看板。

## 运行样例

```
$ go run ./cmd/evidence-demo
== scenario A: clean package, out-of-order chunk arrival ==
version=v1 ok=true
  EMPTY_FILE version="v1" notes.txt: declared empty file; digest matches empty content
== scenario B: tampered digest, dangling reference, missing file ==
version=v2 ok=false
  BAD_DIGEST version="v2" app.log: assembled digest f66b3010…, manifest pins a4981bce…
  MISSING_FILE version="v2" metrics.prom: declared size=33 sha256=c435ab8f… but no sealed content
  DANGLING_REFERENCE version="v2" trace.jsonl#T9: references app.log:NOPE which does not exist
== telemetry (locally synthesized, replayable via fixture) ==
#01 manifest_accepted  v1
#02 file_sealed        v1 metrics.prom
#03 chunk_buffered     v1 app.log offset=52
#04 file_sealed        v1 trace.jsonl
#05 file_sealed        v1 app.log
#06 manifest_accepted  v2
#07 digest_mismatch    v2 app.log
#08 file_sealed        v2 app.log
#09 file_sealed        v2 trace.jsonl
```

## 测试结果

```
$ go test -v ./...
=== RUN   TestCleanPackageVerifies
--- PASS: TestCleanPackageVerifies (0.00s)
=== RUN   TestMissingFileDistinctFromEmptyFile
--- PASS: TestMissingFileDistinctFromEmptyFile (0.00s)
=== RUN   TestBadDigestReportedSeparately
--- PASS: TestBadDigestReportedSeparately (0.00s)
=== RUN   TestUnknownVersionReportedSeparately
--- PASS: TestUnknownVersionReportedSeparately (0.00s)
=== RUN   TestArrivalOrderEquivalence
--- PASS: TestArrivalOrderEquivalence (0.00s)
=== RUN   TestRecoveryEquivalence
--- PASS: TestRecoveryEquivalence (0.00s)
=== RUN   TestOldChunksCannotSpliceIntoNewManifest
--- PASS: TestOldChunksCannotSpliceIntoNewManifest (0.01s)
=== RUN   TestSameNameDifferentDigestRejected
--- PASS: TestSameNameDifferentDigestRejected (0.00s)
=== RUN   TestDanglingReferenceLocatesSourceRecord
--- PASS: TestDanglingReferenceLocatesSourceRecord (0.00s)
=== RUN   TestReferencesOnlyCheckedAfterTargetVerified
--- PASS: TestReferencesOnlyCheckedAfterTargetVerified (0.00s)
=== RUN   TestTimeRangeMismatch
--- PASS: TestTimeRangeMismatch (0.00s)
=== RUN   TestTelemetryFixtureReplay
--- PASS: TestTelemetryFixtureReplay (0.00s)
=== RUN   TestInputsNeverModified
--- PASS: TestInputsNeverModified (0.03s)
PASS
ok  	evidence	0.240s
```
