# 扩展属性策略快照

安全标签放在扩展属性中，内容更新与标签更新分开可造成错误放行。为Go对象服务写带版本的发布包，内容摘要和安全标签同一事务提交，读者取得绑定两者的不可变句柄；缺标签默认拒绝，标签降级须调用方提供批准版本。用写失败和并发读验证任何读取都对应完整旧版或新版。

## 设计

安全标签不再放在可独立修改的扩展属性里，而是与内容摘要写进同一份版本清单
（`manifest.json`），作为一个不可分割的记录提交。

每个对象在磁盘上的布局：

```
objects/<name>/
  current                 # 原子切换的当前版本指针，内容形如 "v3"
  versions/
    v1/
      manifest.json       # {version, object, digest, label, created_at}
      content.bin         # 原始内容
    v2/ ...
  staging/tx-*            # 暂存目录，提交成功前对外不可见
```

### 原子发布（同事务绑定摘要与标签）

1. `Commit` 由互斥锁串行化，先读取当前版本并决定下一个版本号。
2. 在 `staging/` 临时目录里写 `content.bin`，计算 SHA-256，再写包含
   `{版本, 对象名, 摘要, 标签}` 的 `manifest.json`；两者都用“同目录临时文件 +
   rename”原子落盘。
3. 用一次 `os.Rename` 把完整的暂存目录原子地变成 `versions/vN`。
4. 再用一次原子写切换 `current` 指针。切换前读者只看到完整旧版，切换后只看到
   完整新版，任何中间态都不可见。
5. 任一步失败都清理暂存/未发布目录，`current` 仍指向完整旧版，且失败不占用
   版本号。

### 不可变句柄与默认拒绝

- `Open`/`OpenVersion` 不加写锁，直接读取自包含的版本目录，加载时重算内容
  摘要并与清单比对（`ErrDigestMismatch`），再返回 `Handle`。
- `Handle` 是某一版本的不可变快照，`Content()` 返回防御性拷贝；后续提交不会
  改变已返回的句柄。标签与内容因此通过摘要被密码学地绑定。
- `Handle.Authorize(clearance)` 实现默认拒绝：标签缺失返回 `ErrMissingLabel`，
  凭证低于标签等级返回 `ErrDenied`。
- 提交时不允许缺标签（`ErrLabelRequired`）。

### 标签降级审批

当新标签等级低于当前版本时，`Commit` 要求调用方先通过
`GrantDowngradeApproval` 登记一次性批准，批准必须精确匹配对象、当前版本号、
源等级与目标等级，否则分别返回 `ErrDowngradeApproval` 或 `ErrApprovalRejected`；
审批在校验通过后立即消费，不可复用。同级或升级不需要批准。

## 验证

- 写失败：`TestWriteFailureLeavesCompletePreviousVersion` 在内容、清单、发布、
  切指针四个阶段分别注入失败，断言当前版本始终是完整旧版、无残留部分目录、
  版本号不被消耗。
- 并发读：`TestConcurrentReadersSeeOnlyCompleteVersions` 用门闩让提交在临界区
  前暂停，多个读者并发读取，断言每次读取都对应一个完整版本（摘要、内容、标签
  一致），并跨越旧版到新版的边界。
- 防篡改：`TestDigestTamperingIsDetected` 篡改内容字节后读取被拒绝。

```
go test ./... -race -count=1
```
