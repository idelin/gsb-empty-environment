# 备份去重跨租户侧信道

上传服务通过“已存在”快速响应泄漏别的租户是否存过文件。实现供Go存储宿主调用的去重准入组件，物理块可共享，但租户必须完成自己的完整上传与摘要验证后才能获引用；存在性响应只依据本租户已验证索引。用可计数上传器测试跨租户相同内容、失败上传和并发确认，物理复用不能减少必须接收的用户字节。

## 组件

- `AdmissionController`（`admission.go`）：存储宿主调用的准入层，维护
  每租户独立的“已验证摘要 -> 引用”索引。
- `MemoryStore`（`memory_store.go`）：按摘要去重的块存储，带引用计数，
  并暴露 `PhysicalBlocks` / `PhysicalWrites` / `ReferenceCount` 观测点。

## 保证

- **存在性隔离**：`Exists` 只查调用租户自己的已验证索引，从不查询物理
  存储；别的租户（含正在上传中的租户）的块对本租户不可见。非法输入
  （空租户、格式错误的摘要）一律回答 `false`，探测本身不成为预言机。
- **先验证后授权**：`Upload` 必须读完整条上传流并核对 SHA-256 摘要，
  成功后才调用 `BlockStore.Put` 并写入本租户索引。流中断或摘要不符时
  不产生引用、索引条目或物理写入。
- **字节不缩水**：即使摘要相同的块已被其他租户物理存储，新租户仍须
  上传完整对象；`UploadResult.Received` 与可计数上传器都记录实际接收
  字节数，物理复用只影响后端写入次数，不影响必须接收的用户字节。
- **物理共享与引用计数**：相同摘要只持久化一次，每个租户的验证各占
  一个引用；同租户并发重复上传在提交时去重并释放多余存储引用，
  `Delete` 只释放本租户引用，其他租户继续可访问。

## 接口摘要

```go
store := netsec.NewMemoryStore()
ctrl := netsec.NewAdmissionController(store)

res, err := ctrl.Upload(ctx, tenant, wantDigest, requestBody) // 完整接收并校验
ctrl.Exists(tenant, digest)                                   // 仅查本租户索引
rc, err := ctrl.Open(ctx, tenant, digest)                     // 持有引用才可读
err = ctrl.Delete(ctx, tenant, digest)                        // 释放本租户引用
```

宿主可实现 `BlockStore` 接口接入真实后端（对象存储、磁盘池等）；生产
环境可将上传字节先落临时盘再校验，准入保证不变。

## 测试

- `TestCrossTenantDeduplication`：跨租户相同内容只写一个物理块，但双方
  都上传完整字节，且互相无法通过 `Exists` 探测；覆盖共享引用计数与
  删除隔离。
- `TestFailedUploadsGrantNothing`：流中断（可计数上传器在中途报错）与
  摘要不符均不产生任何索引/物理写入，失败后可干净重试。
- `TestConcurrentConfirmations`：8 个租户 × 4 个并发上传，所有 goroutine
  都完整上传，最终只有一个物理块、每个租户一条已验证索引。
- `TestConcurrentExistenceVisibility`：上传进行中另一租户的探测保持否定，
  直到其自身完成验证上传。
- `TestEmptyObjectUpload`：零字节对象的边界行为。

运行：

```sh
go test -race -count=1 ./...
```
