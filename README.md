# 解包后执行页没有绑定原始样本

恶意样本在内存中解密代码后跳转执行，检测器若只给初始文件做摘要，会遗漏实际执行字节。实现供Go沙箱宿主调用的动态代码身份包，使用自定义页事件模型：页大小64字节，事件为Map(pageID,来源摘要,权限)、Write(pageID,偏移,字节)、Protect(pageID,权限)、Execute(pageID,偏移,长度)、Unmap和Remap；同一pageID每次Map产生新代次。Write仅在W权限且范围内，Execute要求X且范围内；任何代次从可写变可执行前必须生成当前64字节SHA256并记录从最近一次非X状态以来的全部写区间，之后再写使旧执行身份失效。W与X同时存在一律拒绝，Remap将新pageID映射到既有物理页并产生独立虚拟代次；Remap共享同一物理页时所有别名权限聚合也须满足W^X，任一别名写会失效全部执行身份。每次Execute输出物理页身份、代次、当前摘要、文件来源和写入来源链；没有有效身份不执行。检测规则回调只接摘要和不可变字节快照，缓存键包含物理页代次。用自解密、两别名、解除后同ID复用、改一字节再恢复及跨页执行测试，核对实际执行的每个字节都有当代摘要，不能把“已解包”当作恶意结论。

## 实现

包 `netsec` 提供三种等价入口：

- 直接调用 `Tracker.Map`、`Write`、`Protect`、`Execute`、`Unmap`、`Remap`。
- 通过 `Apply(MapEvent{...})` 等类型化事件。
- 通过 `Apply(Event{Kind: EventMap, ...})` 的统一事件。

```go
tracker := netsec.NewTracker()

_ = tracker.Map(1, netsec.Source{
    Name:   "packed.bin",
    Digest: sha256.Sum256(packedFile),
}, netsec.PermRead|netsec.PermWrite)

_ = tracker.Write(1, 0, encryptedPage) // 沙箱解密前的数据
_ = tracker.Write(1, 0, decryptedCode) // 解密后的实际执行字节
_ = tracker.Protect(1, netsec.PermRead|netsec.PermExecute)

exec, err := tracker.Execute(1, 0, len(decryptedCode))
if err != nil {
    // 没有当代身份、越界、非 X 或存在可写别名时不会执行。
}

_ = exec.Verify() // Digest == SHA256(exec.Snapshot)，Code 全部被快照覆盖
```

## 不变量

- 页固定为 64 字节；`Write` 和 `Execute` 的区间都必须完全落在页内。
- 单页权限和同一物理页的全部别名权限聚合都必须满足 W^X。
- 进入 X 状态时捕获完整 64 字节快照及其 SHA-256，并携带自最近一次非 X 状态以来的全部写区间和写来源。
- 每次写都会推进物理页代次、记录来源，并使共享该物理页的全部执行身份失效。
- `Remap` 和 `Map` 每次都会分配新的虚拟代次；物理页身份在别名之间共享。
- `Execute` 只返回通过代次和摘要校验的身份。检测回调只收到摘要与独立副本快照；缓存键为 `(物理页身份, 物理页代次, 摘要)`。
- 规则结果为 `unknown`/`benign`/`malicious` 三态。默认是 `unknown`，解包和写代码本身不会产生 `malicious`。

## 验证

`go test -race ./...` 覆盖自解密执行、共享物理页双别名、解除映射后同 ID 复用、改一字节再恢复、跨页执行、规则快照不可变和缓存失效。
