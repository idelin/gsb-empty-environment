# NFS 文件句柄跨导出代次安全

`example.org/netsec/q0215`（包名 `netsec`）提供可直接嵌入 Go NFS 服务端的
文件句柄编解码与对象生命周期管理。句柄是 AES-256-GCM 密封的 opaque
capability，不把 `(fsid, inode)` 当作对象身份。

## 安全不变量

- AEAD 明文包含 export UUID、export generation、后端 stable object ID、
  object generation、对象类型和 policy digest。
- AAD 绑定服务器实例名和 `FormatVersion`；换服务器实例无法重放句柄。
- `Codec.Open` 只返回 `*Capability`，不暴露任何明文 claim。LOOKUP/OPEN/
  READ/WRITE 都必须经过 `Manager.Check`、`Manager.Acquire` 或
  `Lease.Commit`，重新核对导出活跃性、对象代次和当前租户策略。
- 导出删除/替换、快照回滚、对象删除重建分别返回
  `ErrStaleExport`、`ErrStaleSnapshot`、`ErrStaleObject`。
- 旧密钥只在宽限期内验证旧代；过期返回 `ErrKeyExpired`，旧密钥句柄指向
  轮换后创建的新代返回 `ErrOldKeyNewObject`，不会授权新对象。
- `Lease` 固化物件代次快照。并发 DELETE/OPEN 或迟到的存储回调只能得到
  stale 结果，不能写入复用后的新对象。
- 合法硬链接共享同一个 stable object ID、generation 和策略；增加/删除
  某个链接不改变句柄指向，最后一个链接删除后才陈旧。

## 使用

```go
secret := make([]byte, 32) // 从密钥管理系统加载 32 字节随机密钥
keys, _ := netsec.NewKeyRing("key-2026-11", secret, 24*time.Hour)
mgr, _ := netsec.NewManager(netsec.ManagerConfig{
    ServerInstance: "nfs-gw-01",
    KeyRing:        keys,
})

exportID, _ := netsec.NewExportID()
_, _ = mgr.CreateExport(netsec.ExportSpec{ID: exportID, Tenant: "tenant-a", Fsid: 7})
obj, _ := mgr.CreateObject(netsec.ObjectSpec{
    ExportID: exportID,
    StableID: "backend-object-uuid",
    Inode:    1001,
    Type:     netsec.ObjectTypeFile,
    Policy:   netsec.Policy{Tenant: "tenant-a", Allowed: netsec.OpAll},
})

handle, _ := mgr.IssueHandle(exportID, obj.StableID)
cap, _ := mgr.ParseHandle(handle)          // opaque capability
lease, _ := mgr.OpenHandle(cap)            // checks OPEN policy + generations
data, _ := lease.Read()                    // re-checks READ policy
_ = lease.Write(newData)                   // re-checks WRITE policy

err := mgr.Check(cap, netsec.OpLookup)     // LOOKUP pre-check
status := netsec.StatusOf(err)             // STALE / ACCESS / BADHANDLE / ...
```

对象代次由 `Manager` 单调分配，删除重建和快照回滚都会推进代次；导出
generation 在导出替换后推进。即使复用相同 `fsid`/`inode` 甚至相同 stable
ID，旧句柄仍只能稳定失败，不会换绑到新租户对象。

`Lease.Commit` 的存储回调在 Manager 锁内校验代次快照后才执行。回调应当
短小，且不要在回调内再次调用同一个 Manager。

部署时，export/object generation、失效代次 tombstones 和 KeyRing 状态必须
持久化并在重启后恢复；不得在重启时把代次重置为 1，否则旧句柄可能重新变为
有效。多节点网关需要共享同一份代次与密钥状态。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖相同 fsid/inode 重建、跨租户搬运、密文/AAD 篡改、快照回滚、
密钥轮换宽限期与截止代次、并发删除/OPEN 迟到回调和合法硬链接。
