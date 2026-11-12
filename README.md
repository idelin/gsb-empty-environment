# 异常样本队列公平重试

安全测试样本中的基础设施故障允许重试，真实崩溃不能因此被覆盖。实现供Go测试宿主调用的结果队列，尝试分类由显式接口给出，只有基础设施失败进入有限重试，样本最终结果保留所有尝试；优先级相同按首次入队顺序，重试不能饿死新样本。用故障分类变化与并发领取验证不重复执行同一尝试。

## 设计

- `Classifier` 是宿主必须实现的显式分类接口：`ClassInfrastructure` 是唯一可重试类别；
  `ClassCrash` 及其余类别立即终止，崩溃不会被重试掩盖。
- 每个样本有总尝试预算 `maxAttempts`（首次尝试 + 至多 `maxAttempts-1` 次重试）。
  预算耗尽且始终是基础设施故障时，以 `StatusInfrastructureExhausted` 终止。
- 终端 `Result` 按执行顺序保留全部 `AttemptRecord`，包括被重试的失败尝试。
- 调度按 `Sample.Priority` 降序，同优先级按入队序号 FIFO；重试以新的入队序号回到
  该优先级队尾，因此重试期间新入队的样本一定先于重试被领取，重试无法饿死新样本。
- 每个 `(SampleID, AttemptNumber)` 至多发放一次租约，`Complete` 拒绝未知或已消费的
  租约，保证同一尝试不会被执行或记录两次。

## 用法

```go
q := netsec.NewQueue(netsec.ClassifierFunc(func(a netsec.Attempt, err error) netsec.FailureClass {
    if errors.Is(err, errInfra) {
        return netsec.ClassInfrastructure
    }
    return netsec.ClassCrash
}), 3) // 每个样本最多 3 次尝试
defer q.Close()

_ = q.Enqueue(netsec.Sample{ID: "case-1", Priority: 10, Payload: fixture})

lease, err := q.Claim(ctx)
// ... 执行 lease.Attempt 对应的测试 ...
err = q.Complete(lease, ok, cause)

result := q.Result("case-1")           // 终止后包含全部尝试
results := q.Results()                 // 所有终端结果的快照
```

## 验证

```sh
go vet ./...
go test -race -count=1 ./...
```

测试覆盖：基础设施重试后成功且历史完整、崩溃立即终止、重试预算上限、未知分类不重试、
优先级与同优先级 FIFO、重试不饿死新样本、16 worker 并发领取 200 样本无重复尝试、
租约重放/伪造被拒绝、阻塞取消与关闭语义。
