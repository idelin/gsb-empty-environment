# 取证日志撤销记录恢复

取证服务重建文件系统快照时，事务中的撤销记录必须阻止旧日志覆盖后来重用的块。写供Go取证宿主调用的恢复包，输入带序号的事务描述、块更新、撤销表与提交标记；只重放完整提交且校验通过的事务，撤销块使此前同块更新失效但不影响其后新更新。序号按32位模空间且窗口小于半圈。用完整重放参考验证跨回绕、重复日志和未提交撤销不能生效。

## 用法

日志以 `Entry` 流输入：每个事务由 `KindDescriptor`（携带校验和）、若干
`KindUpdate`/`KindUndo`（按 `Index` 排序）与 `KindCommit` 组成。条目可乱序、
可重复投递；提交标记必须是该事务在流中的最后一条。

```go
r := netsec.NewRecoverer()
err := r.IngestAll(entries)      // 或逐条 r.Ingest(e)
snap := r.Snapshot()             // map[netsec.BlockID][]byte
```

- 只有完整提交且 CRC 校验通过的事务会被重放；未提交事务的撤销不生效。
- 撤销记录使此前同块更新失效，但不影响其后的新更新。
- 序号按 32 位模空间排序；已提交序号窗口达到半圈（2^31）时返回
  `ErrWindowExceeded`。
- `ReferenceSnapshot` 是完整重放参考实现，`Recoverer.Snapshot` 在任意日志上
  与其结果一致（见 `recoverer_test.go` 的随机化对比测试）。
