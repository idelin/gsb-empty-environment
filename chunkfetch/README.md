# chunkfetch

从**可注入的随机访问源**读取分块对象的 Go 实现（仅标准库）：

- 按块读取，每块先做**分帧校验**（传输层 SHA-256），再做**清单校验**（块 SHA-256）。
- 一块损坏只重取该一块；其它已验证块保留。
- **恢复位图**持久化进度，断点续传只取缺失块。
- 全部块拼装后做**整体 SHA-256 校验**，通过后才原子写出输出与 `COMPLETE` 完成标记。
- 源的**版本变化**时废弃旧进度（位图、块零件、输出、标记全部清除）；旧版本已验证块
  存放在版本隔离目录中，物理上不可能拼进新版本。
- 字节输入允许任意分块到达；解析明确区分**缺数据**（`ErrNeedMore`，可重试）与
  **坏数据**（`ErrBadFrame`，补再多字节也无用）。
- 在分配载荷内存**之前**检查长度额度，超长声明直接判坏数据，不分配大缓冲。

## 目录

- `protocol.go` — 帧编解码与流式读取（缺数据/坏数据、长度额度）。
- `source.go` — `Manifest`、`Source` 接口、版本不匹配错误。
- `fetcher.go` — 下载/重试/装配/最终校验/完成标记主流程。
- `state.go` — 恢复位图、进度文件、完成标记、原子写、版本目录、旧进度擦除。
- `simsource/` — 内存随机访问源，可脚本注入损坏/截断/超长故障、随机碎片读、版本翻转。
- `cmd/chunkdemo/` — 端到端演示。
- `*_test.go` — `go test` 测试。

## 帧协议

```
[ 4 字节大端载荷长度 ][ 32 字节载荷 SHA-256 ][ 载荷 ]
```

解析语义：

- 只读到帧的一部分（连接切断）→ `ErrNeedMore`（缺数据，重试可恢复）。
- 校验和不符 / 载荷被改 / 完整帧后有多余字节 → `ErrBadFrame`（坏数据）。
- 声明长度超过额度（默认额度 = 该块长度）→ `ErrBadFrame`，且在读取/分配载荷之前判定。
- 干净的流末尾 → `io.EOF`。

## 磁盘布局

```
<Dir>/
  progress.json                 # 版本 + 块数 + 恢复位图（置位=该块已验证）
  COMPLETE                      # 完成标记（仅最终整体校验成功后写出）
  object.bin                    # 装配输出（校验通过后原子 rename）
  chunks/<hex(version)>/
      chunk-000000.part         # 已验证块，原子 temp+rename 落盘
      ...
```

关键规则：

- 块零件按版本目录隔离；`progress.json` 或 `COMPLETE` 中的版本与当前清单不同时，
  整个工作目录被擦除后重新下载。
- 中途读到 `VersionMismatchError`：立即中止，擦除旧进度，返回错误；下次运行拉新版本。
- 恢复时还会重新哈希磁盘上已置位的块零件，发现损坏则清位重取。
- `COMPLETE` 存在时仍会重算输出文件的整体摘要，不匹配则擦除重来（不信任标记本身）。
- 任何块重试耗尽 / 最终整体摘要不符：不写 `object.bin`（临时文件删除）、不写 `COMPLETE`。

## 运行

需要 Go 1.21+（本机演示使用 1.24）。

```sh
cd chunkfetch

# 全部测试
go test -v ./...

# 竞态检测
go test -race ./...

# 端到端演示
go run ./cmd/chunkdemo [输出目录]     # 默认使用系统临时目录
```

## 测试结果

`go test -v ./...`（Go 1.24.2，darwin）：

```
=== RUN   TestEncodeDecodeRoundTrip
=== RUN   TestDecodeFrameByteByByte
=== RUN   TestDecodeFrameMissingVsBad
=== RUN   TestReadFrameLengthQuotaCheckedBeforeAllocation
=== RUN   TestReadFrameStreamedInFragments
=== RUN   TestManifestValidate
=== RUN   TestHappyPath
=== RUN   TestEmptyObject
=== RUN   TestCorruptChunkRetriedOnlyThatChunk
=== RUN   TestTruncatedAndOversizeRetried
=== RUN   TestPersistentFailureNoMarker
=== RUN   TestResumeFetchesOnlyMissingChunks
=== RUN   TestCorruptStoredPartRefetched
=== RUN   TestVersionChangeWipesOldProgress
=== RUN   TestMidRunVersionFlip
=== RUN   TestOldVersionPartsNeverMixed
=== RUN   TestMarkerShortCircuit
=== RUN   TestTamperedOutputInvalidatesMarker
=== RUN   TestMarkerVersionMismatch
--- PASS: ...
ok  	chunkfetch    (coverage: 69.8% of statements)
```

`go test -race` 无数据竞争报告。

## 演示样例

`go run ./cmd/chunkdemo` 包含三个场景（输出有删减，完整见实际运行）：

```
== Scenario A: corrupt chunk retried, truncated chunk retried ==
    chunk 2: attempt 1/4
    chunk 2: chunk 2 frame: chunkfetch: malformed frame      # 坏数据
    chunk 2: attempt 2/4
    chunk 2: chunk 2 frame: chunkfetch: malformed frame
    chunk 2: attempt 3/4
    chunk 2 verified and committed                          # 仅重取第 2 块
    chunk 4: chunk 4 frame: chunkfetch: incomplete frame: need more bytes  # 缺数据
    chunk 4: attempt 2/4
    chunk 4 verified and committed
version=v1 chunks=7 outputBytes=2000 digestOk=true
    marker present and final digest verified; nothing to do  # 完成标记短路

== Scenario B: permanently corrupt chunk, then resume ==
run failed as expected: chunkfetch: chunk 1 failed after 4 attempts: ...
COMPLETE marker written? false (expected false)             # 失败不写完成标记
    chunk 1: attempt 1/4                                     # 续传：只补缺失块
    chunk 1 verified and committed
version=v1 chunks=7 outputBytes=2000 digestOk=true

== Scenario C: source version changes; old progress discarded ==
part directories after v1: [7631]                           # hex("v1")
    marker belongs to an older version; wiping old progress
version=v2 chunks=5 outputBytes=1500 digestOk=true
part directories after v2: [7632]                           # 旧块目录已删除
```
