# 多租户分区字典 LZ 压缩器

本包（`example.org/netsec/q-006`，包名 `netsec`）实现一个按
`(租户ID, Public|Confidential)` 分区的字典压缩器。攻击者可以提交自己的
消息并观察压缩后长度，因此任何其他租户、或其他标签的已提交明文都不得
参与该攻击者流的匹配，也不得影响其压缩输出的一个字节。

## 线格式与匹配规则

压缩结果只包含两种 token：

```
literal := 0x00, 原字节
match   := 0x01, 距离高字节, 距离低字节, 长度
```

- 窗口 `WindowSize = 4096` 字节。
- 最短匹配 `MinMatch = 3` 字节，最长匹配 `MaxMatch = 255` 字节。
- 距离从 1 开始，必须落在窗口内。
- 在当前位置搜索所有候选，最长匹配优先；等长时选最小距离。
- 允许重叠匹配：`distance=1, length=255` 可以表示连续重复字节，解码器
  逐字节复制。
- 文本（literal）按原字节编码；匹配不会跨过窗口边界。

## 隔离与生命周期

- 字典按租户 ID 和标签双重隔离；不同租户、不同标签之间没有共享的字典
  缓冲区、哈希索引或压缩器状态。
- 每个流在 `OpenStream` 返回时取得不可变字典快照。流在打开后看到的字典
  不会因为其他流随后提交而改变；该流自己提交的数据也不会回灌到本次压缩。
- 成功的 `Finish` 在分区锁内把该流明文追加到对应分区。**提交的线性化点
  是这次追加**：在该点之后取得快照的流（包括 `Finish` 返回后打开的所有
  流）能看到新字典；提交流自身以及在该点之前已打开/已取快照的流看不到。
- `Cancel`、上下文取消、压缩/解码错误、`ResetTenant`、`ReleaseTenant`
  以及租户 ID 释放后重新注册，都不发布任何待提交明文。
- `ResetTenant` 和 `ReleaseTenant` 会使 epoch 失效，旧 `Version` 的
  解码返回 `ErrStaleDictionary`；已在旧分区中打开的流在 `Finish` 时失败。
  压缩过程中还会周期性检查 epoch，使重置后的旧字典工作尽快停止。
- **租户 ID 复用必须先 `ReleaseTenant`（或 `RemoveTenant`）再重新注册/
  打开。** 释放会删除旧映射并撤销旧 epoch；之后同名 ID 自动获得全新空
  分区。若应用在未释放的情况下把同一 ID 交给新主体，存储层无法区分二者。

## 基本 API

```go
store := netsec.NewStore()

res, err := store.Compress(ctx, "tenant-42", netsec.Confidential, message)
// res.Data 是压缩 token 流；res.Version 是解码所需的字典快照。
plain, err := store.Decompress(ctx, "tenant-42", netsec.Confidential,
    res.Version, res.Data)
```

流式提交：

```go
st, err := store.OpenStream(ctx, "tenant-42", netsec.Public)
_, err = st.Write(message)
res, err := st.Finish() // 只有成功 Finish 才提交字典
// 或 st.Cancel()，不提交且不返回压缩结果。
```

`Compress(dict, src)` / `Decompress(dict, encoded)` 是无状态底层编解码
原语，适合测试或调用方自行管理字典；服务路径应使用 `Store` 方法以执行
租户/标签隔离、epoch 和提交生命周期规则。

## 并发

- 全局互斥锁只保护租户映射和 epoch 分配；整段压缩不持有全局锁。
- 同一分区的多个流可以并行压缩：打开时短暂读锁取快照，提交时短暂写锁
  追加明文。不同分区的流没有共享锁。
- 解码在同一分区持读锁完成，因此 `ResetTenant` 不会与正在使用的旧字典
  交错；同分区多个解码可以并行。

## 测试

```
go test ./...
go test -race ./...
```

测试覆盖：最长匹配/同长最小距离、255 字节上限、重叠匹配、字典边界、
4096 字节窗口滑动、损坏 token 拒绝、同租户合法复用、提交可见性、
取消/上下文取消、重置、租户 ID 释放复用，以及“更换其他租户秘密不改变
攻击者输出”的对照实验和并发流/并发解码。

## 部署注意

- 租户 ID 必须由认证层绑定到真实主体；不要让未认证调用方任意选择他人的
  租户 ID。
- `Decompress` 能按 token 距离读出字典内容，只能暴露给受信内部调用方；
  不要把它和“允许攻击者提交任意压缩 token”同时开放。
- 字典在重置/释放前保留明文历史；本包保证逻辑隔离和 epoch 失效，不承诺
  Go 运行时层面立即清零所有内存副本。
