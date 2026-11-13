# JIT代码先发布但元数据仍是旧版本

运行时更新JIT代码时若代码、栈映射、异常表和调用目标表不是同一代原子发布，线程可能按新指令配旧元数据。给Go运行时宿主实现可导入的代码版本发布包：一个CodeUnit版本含unitID、generation、代码摘要、函数/指令边界摘要、stackMap摘要、exceptionTable摘要、callTarget摘要和父generation；全部内容写入不可变对象并由验证回调分别通过且所有摘要重算匹配后生成manifest根。发布先登记Pending，取全局代码屏障：所有线程回执当前PC和返回地址不在待替换旧代码的可修改区间，或已固定旧generation租约；随后原子把入口表从旧manifest切到新manifest。新调用只取新generation，已有旧帧继续用其manifest直至退出；任何元数据查询必须由帧generation定位，不能取“当前版本”。回收旧版本需所有帧/回调/采样器租约归零。更新失败或崩溃恢复只发布完整manifest，孤立内容回收。实现可控线程交错，搜索待审分步发布中以新code摘要配旧stackMap/exception/callTarget成功执行的最短见证；覆盖OSR入口、递归旧帧、新线程加入、采样器迟到及generation编号复用。

## Implementation

The package is importable as `example.org/netsec/q0168` (package `netsec`).

- `NewUnitVersion` deep-copies every component, canonicalizes ordering, invokes
  the five validation callbacks separately, computes all component digests and
  the unit content root, then recomputes and compares them.
- `BuildManifest` verifies each unit again, rejects duplicate unit IDs, and
  produces the manifest root over sorted unit content roots and the parent
  manifest root. Units and manifests have no setters.
- `Publisher.Begin` stages a complete manifest as `Pending`; no content is
  visible to new calls. `Publisher.Barrier` probes every registered thread and
  accepts either PC/return-PC outside the replaced ranges or a live lease on
  the old manifest. `Publisher.Commit` swaps the entry-table pointer atomically
  under a gate shared with `Runtime.Enter`.
- `Runtime.Enter` pins the current manifest for the activation.
  `Runtime.Call` and `Runtime.OSRFrom` reuse the parent frame's manifest, so
  recursive old frames and on-stack replacement stay on their generation.
  Metadata methods exist only on `Frame`/`Runtime` with a frame argument and
  never consult the current manifest.
- `Publisher.TryReclaim` refuses to reclaim a retired manifest until frame,
  callback and sampler lease counts are all zero. `ContentStore` tracks
  immutable blobs and reclaims unreferenced content.
- `Publisher.Recover` verifies a complete manifest, discards pending state and
  thread registrations, and keeps only content reachable from the recovered
  manifest.
- `StagedPublication`, `StagedWorld`, `Explorer` and `Witness` implement a
  deterministic BFS over component-wise publication interleavings. The shortest
  torn witness is six actions: publish new code, enter, look up old stack map,
  old exception table and old call targets, then execute successfully.

## Example

```go
next := /* UnitVersion generation 2 */
initial := /* Manifest with generation 1 */
nextManifest, _ := BuildManifest([]*UnitVersion{next}, initial, Validators{})
p, _ := NewPublisher(initial)
rt := NewRuntime(p)

oldFrame, _ := rt.Enter("hotfunc") // pins the old manifest
_, _ = p.Publish(context.Background(), nextManifest)
newFrame, _ := rt.Enter("hotfunc") // pins the new manifest

oldMD, _ := oldFrame.MetadataAt(0x10) // old code + old stack map/table/targets
newMD, _ := newFrame.MetadataAt(0x10) // new code + new metadata
```

## Verification

```sh
go test ./...
go test -race ./...
```

The test suite covers malformed content and digest tampering, barrier rejection
and lease pinning, thread joining during a barrier, atomic entry-table swap,
OSR entry, recursive old frames, late samplers, generation-number reuse,
reclaim gating by all three lease classes, crash recovery/orphan reclamation,
and the shortest torn-execution witness with exact replay.
