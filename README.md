# 实时迁移漏传最后一轮脏页

虚拟机预复制迁移若在停止源端前清错脏位，目标机会以旧内存启动，安全策略或密钥状态回退。给Go迁移宿主实现可导入的内存复制状态机：内存最多64页，每页有内容摘要、写代次和所属VM；源端写页时原子增加代次并置dirty。每轮先取得(全局epoch、dirty集合及每页快照代次)快照，复制每页的(页号、代次、摘要、字节)到目标隔离区并回读摘要；仅当源页当前代次仍等于复制代次时可清对应dirty，否则保持。切换阶段先冻结新设备DMA和vCPU写，等待全部在途写完成，再取最终dirty和设备状态，复制并验证后生成清单根；目标只在宿主签名回执绑定VM身份、源/目标实例、最终epoch、全部页代次、设备状态摘要和清单根后启动。源端在收到目标启动确认前保持冻结且不可复用内存，确认后永久终止；失败可解冻继续运行，但目标隔离区不可见。实现逐事件故障注入与并发写调度，找待审实现中目标启动页代次低于源冻结点的最短见证。覆盖复制中再写、清dirty与写竞争、重复轮、目标回执丢失、源快照回滚和跨VM清单重放。

## 实现概览

包 `netsec` 提供可直接导入的迁移宿主状态机：

- `Memory`（别名 `Source`）：最多 64 页；每页保存所属 `VMID`、单调写代次、SHA-256 内容摘要、字节和 dirty 位。`WritePageKind` 在同一临界区内完成代次递增、摘要更新和 dirty 置位。
- `Target`：隔离区只保存通过内容摘要校验的 `PageRecord`。启动前 `ReadPage` 返回 `ErrQuarantineHidden`，只暴露页数/代次等元数据。
- `Signer`：Ed25519 宿主签名。回执签名覆盖 VM、源/目标实例、最终 epoch、全部 64 页代次、设备状态摘要和清单根。
- `Migrator`（别名 `MigrationHost`）：预复制轮、冻结切换、回执交付、启动确认和失败回滚。
- `EventDispatcher` / `WriteScheduler` / `ConcurrentWrites`：逐事件故障注入与并发写调度。
- `FindShortestStaleStartWitness`：对可执行单页模型做 BFS，返回“目标启动代次低于源冻结代次”的最短动作序列。

## 关键不变量

1. dirty 清除条件只有 `source.pages[p].generation == copiedGeneration`。快照之后发生的写永远保留 dirty，下一轮必须重传。
2. 目标隔离区按代次单调接收记录；旧快照不能覆盖新页，同代次不同摘要直接拒绝。
3. `Freeze` 先关闭写入口，再等待 `inFlight == 0`，最后推进 epoch 并抓取最终 dirty、全部页代次/摘要和字节。
4. 清单根覆盖全部 64 页（未使用页代次为 0）和设备状态摘要；回执签名覆盖清单根及全部页代次。
5. `Target.Start` 要求每页 `target.generation == manifest.generation` 且摘要一致。缺失页或旧代次返回带 `PageMismatch` 的 `StaleStartupError`，目标不可见。
6. `Cutover` 成功后源端冻结被 seal；直接 `Thaw` 返回 `ErrSealed`。只有 `Migrator.Abort` 在确认目标未启动后能解冻。目标启动后 `Abort` 返回 `ErrTargetStarted`，必须走 `ReconcileStartup`。
7. 收到与回执绑定的启动确认后源端永久 `Terminated`；`ReleaseMemory` 之前不可复用。

## 基本用法

```go
src := netsec.NewSource("vm-42", "vm-42/src/7")
dst := netsec.NewTarget("vm-42", "vm-42/dst/1")
hostKey, _ := netsec.NewRandomSigner()
host, err := netsec.NewMigrator(src, dst, hostKey)
if err != nil {
    return err
}

_, _ = src.WritePage(0, bootBytes)
_, _ = src.WriteDevicePage(1, dmaBytes)

_, err = host.RunPreCopy(8)             // 轮次快照 + 回读 + 代次校验清 dirty
handoff, err := host.Cutover(deviceState) // freeze, wait in-flight, final copy, manifest, sign
confirmation, err := host.StartTarget()   // target verifies receipt + every page generation
err = host.ConfirmStartup(confirmation)   // source terminates permanently
_ = host.ReleaseMemory()
```

失败路径：

```go
if _, err := host.StartTarget(); err != nil && !dst.Started() {
    _ = host.Abort("receipt lost") // thaw source, invalidate target quarantine
}
if dst.Started() {
    _, _ = host.ReconcileStartup() // recover a lost startup confirmation
}
```

## 故障注入与并发

每个协议步骤都发出 `EventName` + `EventPhaseBefore/After`。`ScriptedFaults` 可按精确 occurrence 注入错误；`WriteScheduler` 可在指定事件上同步或异步写页，制造“复制中再写”和“清 dirty 与写竞争”。

```go
d := netsec.NewEventDispatcher(nil)
host, _ := netsec.NewMigrator(src, dst, signer, netsec.WithMigrationInjector(d))

sched := netsec.NewWriteScheduler(src)
sched.ScheduleSync(netsec.EventPageCopyBegin, netsec.EventPhaseBefore,
    1, 0, []byte("new-value"), netsec.WriteVCPU)
d.SetFault(netsec.FaultFunc(sched.Handle))

round, _ := host.Round() // dirty 不会被旧快照清掉
```

`ConcurrentWrites` 用统一起跑线并发写同一页，验证代次唯一且最终摘要与冻结快照一致。

## 安全审查见证

```go
w, ok := netsec.FindShortestStaleStartWitness(netsec.SafeAuditPolicy(), 12)
// ok == false：规范实现不存在旧代次启动

w, ok = netsec.FindShortestStaleStartWitness(netsec.LegacyUnsafeAuditPolicy(), 12)
// write -> round.snapshot -> write -> dirty.ack -> freeze.final -> target.start
// 目标页代次 1 < 源冻结点代次 2
```

`LegacyUnsafeAuditPolicy` 同时建模两个历史缺陷：清 dirty 时不复核代次，以及目标启动时不校验清单代次。BFS 保证返回的是最短动作序列。

## 测试

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖：复制中再写、dirty 清除与写竞争、重复轮/重复记录、回执丢失、启动确认丢失、源快照回滚、跨 VM 清单/页重放、设备状态回滚、逐事件故障注入、并发写与冻结竞争。
