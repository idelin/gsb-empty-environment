# blockpack

带完整性证明的分块对象包格式。纯 Go 标准库实现，无第三方依赖。

## 设计

- **对象编码**：显式长度前缀二进制格式
  （`u32 len(Type) | Type | u32 len(Data) | Data | u32 len(Refs) | u32 Refs...`）。
  解码是纯数据操作，不解析类型、不执行宿主代码；每个长度字段先对照硬上限
  （`MaxTypeLen`/`MaxDataLen`/`MaxRefs`/`MaxObjects`/`MaxBlocks`）和剩余输入
  校验，通过后才分配内存。
- **根摘要**：`root = H(version, objectCount, blockCount, blockSize, chain,
  refGraph, merkleRoot)`，同时承诺对象顺序与数量（链式折叠）、引用图
  （逐对象提交其引用列表）和块 Merkle 树。
- **块摘要**：每块携带 `Digest`（绑定块序号、块内对象数、有序对象摘要）和
  `SummaryRoot`（整包对象级根摘要），边读边校验时逐块对照根摘要。
- **流式验证**：`Verifier.AddBlock` 逐块校验，`Finish` 前只报告
  `LocallyVerified()`（局部已验块数），`Complete()` 恒为 false；
  `Finish` 重新累计全部证据并重算根摘要后才宣告整包完整。删末块、
  块换序、缺对象都会在读取时或 `Finish` 时失败。
- **范围证明**：`ProveRange(start, end)` 返回范围内块 + Merkle 兄弟路径 +
  链/引用图承诺；`Verify(expectedRoot)` 重算根并比对，旧包的证明无法拼进新包。
- **版本隔离缓存**：`Cache` 以 `(version, blockIndex)` 为键，且仅当当前包
  摘要承诺的摘要与缓存一致才命中；旧版本缓存块不可能无复核复用。
- **独立验证器**：`VerifySubset` 校验任意块子集并返回失败块序号，
  `MatchTamperManifest` 与篡改清单精确比对。

## 运行

```sh
go test ./...      # 运行测试
go run ./cmd/demo  # 运行演示
```

## 运行样例（go run ./cmd/demo）

```
built package: version=1 objects=5 blocks=3
root digest: 4931791770811f06b1fe2d0a2aa7d916
wire format: 430 bytes, parsed back OK
block 0 locally verified (complete=false)
block 1 locally verified (complete=false)
block 2 locally verified (complete=false)
finish: package complete=true
range proof blocks [1,3): verify=true
last block deleted: finish err=blockpack: incomplete package: 2 of 3 blocks verified
block swap: add err=blockpack: block 1 out of order, expected block 0
cache: v1 block hit=true, v2 block hit=false (must re-verify)
old proof spliced into v2: verify=false
tamper check: failed blocks=[1], matches manifest=true
```

## 测试结果（go test ./...）

```
ok  	blockpack	0.104s
```

14 个测试全部通过，覆盖：往返流式验证、删末块失败、块换序失败、
篡改对象失败、缺失必要对象不可被局部证据掩盖、异版本块摘要拒绝、
范围证明及其篡改检测、旧证明不可拼接到新包、缓存版本隔离、
任意子集篡改清单核验、长度先验后分配、截断/尾随垃圾拒绝、
引用图承诺。
