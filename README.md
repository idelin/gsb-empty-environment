# 虚拟设备 DMA 隔离

`netsec` 是一个可导入的纯内存 IOMMU 页表模型。它不访问真实硬件或进程内存，适合作为设备沙箱的地址翻译层和并发/安全测试基础。

## 安全模型

- 页大小固定为 4096 字节，`PhysPage` 是页帧号而不是真实虚拟内存地址。
- 每个设备注册时获得独立 `DomainID`，并有自己的 `Generation`。
- 宿主控制面通过 `AssignPage`/`AssignPages` 建立不可变物理页归属表。
- 设备只能提交 `Transfer` 请求；设备 ID、IOVA、长度、散布聚集列表和访问权限均视为不可信输入，不能修改页归属。
- 映射带 `Read`/`Write` 权限。映射或重映射时，物理页必须已归属该设备所属租户；同一物理页不能归两个租户。
- 传输先验证整张列表和全部页映射，再一次性取得所有页租约，最后才逐页调用内存回调。缺失映射、权限不足、地址溢出或混域都会在零回调的情况下失败。
- `Remap` 推进设备代次并使旧翻译失效；已经取得租约的传输继续使用旧页完成。`Unmap`/`Revoke` 立即移除当前翻译、阻止新传输，并等待当前及历史翻译上的旧租约全部结束后返回。

## 使用

```go
m := netsec.New()

// 宿主控制面：租户、物理页和设备均只能由宿主建立。
_ = m.AddTenant(1)
_ = m.AssignPages(1, 100, 2) // 页帧 100、101
_ = m.AddDevice(7, 1)
_ = m.MapRange(7, 0x1000, 2*netsec.PageSize, 100, netsec.Read|netsec.Write)

// 设备侧：请求可以跨页；回调按页拆分并携带物理页帧及页内偏移。
result, err := m.Transfer(ctx, netsec.TransferRequest{
    Device: 7,
    IOVA:   0x1fe0,
    Length: 0x40,
    Access: netsec.Read,
}, func(op netsec.AccessOp) error {
    // op.PhysPage、op.PhysOffset、op.Length、op.Generation 已完成验证。
    return nil
})
```

`SG` 非空时，每个 `Segment.IOVA` 是请求窗口 `[IOVA, IOVA+Length)` 内的绝对地址；所有段长度之和必须等于 `Length`。段按列表顺序处理，但仍需整表验证通过后才会回调。

`Unmap`/`Revoke` 是阻塞操作。不要在同一个传输的 `AccessFunc` 内撤销它自己持有的租约，否则会按设计等待并死锁。

## 测试

```sh
go test ./...
go test -race ./...
```

测试通过通道精确控制回调暂停和释放，覆盖重映射/撤销竞态、SG 列表后半项越权、旧代次租约、撤销等待，以及同物理页跨租户别名尝试。
