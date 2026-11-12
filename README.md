# 遗忘请求与模型版本

特征服务删除个体数据时，异步训练回调可能发布仍包含该个体的旧模型。写供Go隐私宿主调用的发布屏障包，训练任务固定输入清单与删除代次，发布前检查所有删除事件都已反映；过期训练只能重建，不能靠换版本号放行。用暂停训练器测试删除、取消和模型发布的先后，保留被拒模型清单证据。

## 发布屏障（`netsec` 包）

`Barrier` 是隐私宿主在模型上线前必须经过的闸门。每个训练任务在启动时
固定两份不可变事实：

- **输入清单** `Manifest`：该任务允许读取的全部个体 ID，附带规范化
  SHA-256 摘要，换输入即换摘要。
- **删除代次**：启动瞬间 `DeletionLog` 的最新代次。删除日志只增不改，
  每次删除个体数据分配一个递增代次。

异步训练回调调用 `Barrier.Publish` 时，模型只有同时满足以下条件才放行：

1. 任务存在、仍在运行且未被取消；
2. 模型携带的清单摘要与输入均与启动时固定的清单一致；
3. 固定代次之后没有新的删除事件，且清单中不含任何已删除个体。

任何拒绝都会写入只增的被拒模型台账（`Rejected`），保留模型版本、拒绝
原因、缺失的删除事件、残留的已删除输入与时间戳，可作为审计证据；重建
任务不会抹除历史记录。

### 过期训练只能重建

模型的 `Version` 是不受信任的元数据，不参与任何放行判断。任务一旦落后
于删除代次，其固定契约无法修补，换版本号重新发布会因任务已终结而再次
被拒。唯一出路是 `Rebuild`：以删除后的新清单启动新任务、固定新代次并
重新训练。

### 典型用法

```go
deletes := netsec.NewDeletionLog()
barrier := netsec.NewBarrier(deletes)

manifest := netsec.NewManifest("ind-1", "ind-2")
task, _ := barrier.StartTask("train-42", manifest)

// ... 异步训练期间可能发生 deletes.Record("ind-2") ...

decision := barrier.Publish(netsec.Model{
    TaskID:         task.ID(),
    Version:        "2026.11.12-3",
    ManifestDigest: manifest.Digest(),
    Inputs:         manifest.Inputs(),
})
if !decision.Allowed {
    // decision.Reason 说明原因；证据已进入 barrier.Rejected()。
    clean := netsec.NewManifest("ind-1")
    rebuilt, _ := barrier.Rebuild(task.ID(), "train-43", clean)
    _ = rebuilt // 重新训练后再 Publish
}
```

### 测试

`barrier_test.go` 中的 `pausableTrainer` 是可暂停的训练器测试替身，用来
确定性地控制删除、取消与回调发布的先后顺序，覆盖：

- 训练中途删除个体，迟到回调被拒并保留删除证据；
- 训练中途取消任务，迟到回调与重试均被拒；
- 先取消上下文再释放，任务退出且不产生任何发布；
- 删除后仅提升版本号无法放行，只有重建可恢复；
- 清单摘要/输入被调包、未知任务、重复发布等拒绝路径；
- 删除与发布并发时，台账与任务终态在竞态检测下保持一致。

运行：`go test -race -count=1 ./...`
