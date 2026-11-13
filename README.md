# 崩溃安全的 inode 链接事务

包 `netsec` 提供可导入的 inode/目录事务宿主。目录项和 `linkCount` 在同一个
HMAC-SHA256 认证日志事务中提交；恢复仅应用完整提交，并按目录树重建引用集合。

## 使用

```go
h, err := netsec.NewHost(netsec.Options{
    LogPath: "var/links.log",
    AuthKey: key, // 至少 16 字节
})
if err != nil {
    return err
}
defer h.Close()

dir, _ := h.CreateDirectory(netsec.PermAll)
ino, _ := h.CreateInode(netsec.CreateInodeOptions{Data: data})
if err := h.Link(ino, dir, "file"); err != nil {
    return err
}

entry, _ := h.Lookup(dir, "file")
lease, _ := h.Open(entry)
if err := h.Unlink(entry); err != nil { // ino 变为 Orphan，leased 数据保留
    return err
}
if err := h.CloseLease(lease); err != nil { // 最后一个租约关闭后才可 Deleting
    return err
}
```

## 恢复保证

- `Link` 先校验源 inode 的策略代次/`PolicyAllowLink` 和目标目录的
  `PermCreate`，再原子写入目录项与递增后的 `linkCount`。
- `Unlink` 只删除与目录代次、规范名、inode ID 和 inode 代次完全匹配的项。
- `Open` 租约绑定 inode 代次；策略撤权立即拒绝新 Open，已有租约按
  `RevokeDrain`、`RevokeGrace` 或 `RevokeEnd` 结束。到期 grace 租约会在
  `ExpireLeases`、`Sweep` 或恢复回收时被关闭。
- 恢复逐 inode 比较实际目录项数与 `linkCount`。不一致、重复目录项、悬空或
  过期代次引用都会隔离，且不会把计数改成较小的扫描值。
- 只有 `linkCount=0`、`openCount=0`、独立全目录扫描零引用且未决事务已回滚时
  才进入 `Deleting`。回调前会再次全量扫描，释放事务提交后 inode 代次递增。

默认会自动清理可回收 inode。设置 `Options.DisableAutoReclaim` 可改为显式
调用 `Sweep()`。
