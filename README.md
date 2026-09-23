# chunkproof —— 分块摘要 + 对象级根摘要的完整性证明

纯 Go 标准库实现（无第三方依赖）。包文件可能来自不可信磁盘/传输；**唯一可信的是带外分发的 32 字节根摘要**。
支持「按范围读取、边读边校验」，在 `Finish` 成功之前，验证器永远只声明**局部已验**。

## 安全性质

- **块摘要 + 对象级根摘要**：每个分块绑定 `SHA256("proof/chunk/v1" ‖ index ‖ bytes)`；根摘要
  `SHA256("proof/root-summary/v1" ‖ canonical(manifest))` 一次性承诺：包 id、版本、分块大小、
  **分块数量与顺序**、每个分块哈希、**对象数量与顺序**、每个对象的偏移/长度/元数据、**完整引用图**。
- **局部证据不能掩盖缺失必要对象**：清单几何校验要求对象按偏移连续覆盖分块负载，引用必须闭包；
  少列对象、悬空引用、对象换序都在验证器构造时拒绝；`Finish` 再核对对象数量与逐条对象记录。
- **按范围读取的完整性证明**：`ReadRange` / `ReadObject` 只拉取覆盖范围的分块，逐块哈希校验，
  返回 `RangeEvidence`（根 + 每块哈希与来源），可对任意块子集独立核验。
- **版本隔离缓存**：缓存键绑定 `sha256(pkgID) ‖ version ‖ index ‖ chunkHash`；旧包/新版/哈希变化均
  未命中；**命中也必须重算哈希**，被投毒或旧版缓存块不可能无摘要复核地复用。
- **旧证明不能拼到新包**：版本号在清单内，根摘要覆盖版本字段；v1 的根验 v2 必失败。
- **finish 前只标局部**：状态机 `local → complete`；`Finish` 要求恰好全部块（数量与索引）+ 全部对象。
- **删除末块不能因前块都正确而通过**：`Finish` 检查 `len(seen) == len(chunks)` 且索引 0..n-1 齐全。
- **块换序失败**：哈希输入含分块 index，块 2 的字节按索引 1 提交必哈希不符。
- **解码不执行宿主代码**：全部记录为显式定长二进制（无 gob/反射分发/接口载荷/插件）。
- **先验证长度再分配**：每个 `Scope` 有硬上限；读取前 `CheckScope`；长度前缀字段和元素计数都先与
  剩余字节、硬上限比较再 `make`；负数长度直接拒绝。

## 支持的序列化范围（`proof.Scope`）

| Scope | 记录 | 硬上限 |
|---|---|---|
| `ScopeChunk` | 单分块（index + bytes，见 `ChunkHash` 输入） | 1 MiB |
| `ScopeObject` | 单个规范化对象记录 `OBJ1 ‖ id ‖ kind ‖ name ‖ refs ‖ payload` | 64 MiB |
| `ScopeManifest` | 单个规范化清单 `MFST ‖ ...` | 16 MiB |

三种范围都走同一个 `scopeReader`：限长 → 验 tag → 边界读取 → 计数预校验 → 末尾禁止余留字节。
清单/对象解码后还要求**重新编码逐字节相等**，杜绝非规范化歧义。

## 文件格式（单文件信封，52 字节尾部）

```
payload（对象记录拼接）
canonical manifest（清单）
u64 manifest 长度
[32]byte 根摘要（文件内自报，仅作展示，验证器不因此信任它）
u64 payload 长度
[8]byte magic "PRFENV01"
```

## 代码结构

- `proof/scope.go`：范围定义、硬上限、先验长度再分配的安全解码器（无宿主代码执行）。
- `proof/proof.go`：类型、规范化清单/对象编解码、几何与引用图校验、根/对象/分块摘要。
- `proof/builder.go`：按输入顺序编号对象、拼接、分块、产出根摘要（构建期即闭包校验）。
- `proof/envelope.go`：单文件读写与按索引取块（偏移只由清单几何算出）。
- `proof/verifier.go`：独立验证器与 `local/complete` 状态机。
- `proof/cache.go`：版本/包/索引/哈希四绑定缓存，命中仍重算哈希。
- `proof/ranges.go`：`ReadRange` / `ReadObject` / `ObtainChunk` 与证据结构。
- `proof/*_test.go`：20 个 `go test` 对抗用例。
- `cmd/demo`：端到端演示与攻击表。

## 快速开始

```sh
go test -v -count=1 ./proof   # 单元测试（20 个）
go test -race ./...           # 竞态检测
go run ./cmd/demo             # 端到端演示
```

## 运行样例（本仓库实际输出，darwin/arm64, go1.24.2）

```
=== 1. 构建包（对象顺序/数量/引用图由根摘要承诺） ===
包 id=demo-pkg version=1 对象数=3 分块数=7 负载=419 字节
可信根摘要（带外分发）: 41ea3bcc993b9f6ef97f08bf

=== 2. 独立验证器（不信任文件内自报根，只信带外根摘要） ===
初始状态: local（finish 之前只允许局部声明）

=== 3. 按范围读取任意子集，边读边校验，仅声明局部已验 ===
范围 [50,180) 读取 130 字节，证据分块=[0 1 2]，状态仍为=local
对象 1 ("beta"/"blob") refs=[0] 已通过对象级验证；整包状态=local

=== 4. 未读全时 finish 必须失败（删除末块同理） ===
finish 被拒（符合预期）: proof: not every chunk has been verified: 6 of 7 chunks verified

=== 5. 补齐全部分块（缓存命中复用，复用前仍做摘要复核）后 finish ===
分块 0: fromCache=true
分块 1: fromCache=true
分块 2: fromCache=true
finish 成功: status=complete chunks=7/7 objects=3/3

=== 6. 版本隔离：旧证明不能拼到新包 ===
v1 根=41ea3bcc993b9f6e
v2 根=13e3eb5eb96a6a94
用 v1 根验证 v2 被拒（符合预期）: proof: root summary mismatch (old proof or tampered manifest)
v1 缓存块对 v2 查询未命中：不会无摘要复核复用旧块

=== 7. 攻击表（每种篡改都必须被独立验证器拒绝） ===
- 删除末块（前块全部正确）: 拒绝: proof: not every chunk has been verified: 6 of 7 chunks verified
- 块换序（块2的字节按索引1提交）: 拒绝: proof: chunk content hash mismatch: chunk 1
- 磁盘负载篡改: 拒绝: proof: chunk content hash mismatch: chunk 0
- 清单篡改（换包 id）后仍用旧根: 拒绝: proof: root summary mismatch (old proof or tampered manifest)
- 清单缺失必要对象（局部块证据无法掩盖）: 拒绝: proof: invalid manifest geometry: objects cover 369 payload bytes but chunks hold 419
- 缓存被投毒: 命中前摘要复核失败，按未命中回源并重新验证

全部场景演示完成。
```

## 测试结果（实际输出）

`go test -v -count=1 ./proof`（20/20 通过）：

```
--- PASS: TestCacheVersionIsolation
--- PASS: TestCacheTamperedEntryRejected
--- PASS: TestCacheReuseAvoidsFetcher
--- PASS: TestLengthCheckedBeforeAllocation
--- PASS: TestTrailingAndTruncatedRejected
--- PASS: TestObjectRecordCodecRoundtrip
--- PASS: TestEnvelopeRoundtripAndTamper
--- PASS: TestDanglingRefRejectedAtBuildAndVerify
--- PASS: TestDuplicateAndUnsortedRefsRejected
--- PASS: TestChunkIndexOutOfRange
--- PASS: TestHappyPathFinishComplete
--- PASS: TestDeleteLastChunkFails
--- PASS: TestChunkSwapFails
--- PASS: TestTamperedChunkBytesFail
--- PASS: TestRootSummaryCommitsOrderAndGraph
--- PASS: TestMissingNecessaryObjectCannotBeMasked
--- PASS: TestTamperedManifestWrongRoot
--- PASS: TestOldProofCannotSpliceNewPackage
--- PASS: TestArbitrarySubsetRangeRead
--- PASS: TestLocalObjectEvidencePartial
PASS
ok  	chunkproof/proof
```

`go test -race -count=1 ./...`：`ok chunkproof/proof`（无数据竞争）。
