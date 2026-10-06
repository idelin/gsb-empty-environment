# replicaread

Go 副本读取库：对带版本副本执行仲裁读（quorum read）并做读修复（read repair）。

## 语义

- **版本**：`Version{Term, Seq}`，先比任期再比序号，全序。
- **负载**：每个副本应答一个值（`Value`）或墓碑（`Tombstone`），墓碑照常参与版本比较。
- **仲裁**：应答（含失败应答）达到 `quorum` 后完成一个结果代际，取最高版本的内容。
- **冲突**：同一最高版本出现不同内容时，`Result.Conflict` 为真，`Conflicts` 列出全部不同内容，且不做任何修复。
- **修复**：只发给已确认较旧的副本（即本代际内应答过且版本更低者）；未应答副本不受影响；冲突或仲裁失败时不修复。
- **代际**：结果一旦发布即不可变。迟到应答若版本高于已发布结果，开启新代际重新累积仲裁；旧回执保持不变。迟到的低版本、重复或失败应答直接丢弃。

## 用法

```go
repairs := func(replica string, v replicaread.Version, p replicaread.Payload) { /* 写回旧副本 */ }
r := replicaread.NewReader(3, repairs)

// 应答顺序完全由调用方控制，便于确定性核对：
r.HandleResponse(replicaread.Response{Replica: "a", Version: replicaread.Version{Term: 1, Seq: 1}, Payload: replicaread.Value([]byte("old"))})
r.HandleResponse(replicaread.Response{Replica: "b", Version: replicaread.Version{Term: 2, Seq: 5}, Payload: replicaread.Value([]byte("new"))})
r.HandleResponse(replicaread.Response{Replica: "c", Err: errors.New("timeout")})

res := r.Results()[0] // 版本 2.5 胜出，副本 a 收到修复
```

## 测试

```sh
go test ./...
```
