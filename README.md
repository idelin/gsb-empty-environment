# snapshotstream

Go 快照流库：向每个订阅者投递**最新完整版本**的分片流。

## 语义

- **最新完整版本**：每个订阅者的队列同一时刻只持有一个版本的分片；新订阅者立即获得当前最新版本。
- **可跳过、不拼接**：订阅者积压时，新版本发布会**整体驱逐**队列中的旧版本分片（跳过中间版本）。接收到的分片流保证是若干**连续版本前缀**的拼接：每个版本的片段从下标 0 开始连续到达，版本号严格递增，绝不把不同版本的分片拼接在一起。每个 `Fragment` 携带 `Version / Index / Count`，接收方可识别并丢弃被截断的版本。
- **确认释放**：`Recv` 取走的分片仍计入订阅者的保留量，直到 `Ack(version)` 释放该版本及更早版本的全部已收分片。
- **取消即解引用**：`Cancel` 立即清空队列中和已收未确认的分片，从流的订阅表摘除，并唤醒阻塞中的 `Recv`。
- **容量压力**：队列容量按分片数固定（`NewStream(queueCapacity)`），单版本分片数超过容量会被 `Publish` 拒绝（`ErrVersionTooLarge`）；积压订阅者的内存占用恒定为一个版本的大小，与发布速率无关。

## API 概览

```go
st := snapshot.NewStream(8)              // 每订阅者队列容量 8 个分片
sub := st.Subscribe()                    // 迟到者直接拿到最新版本
st.Publish(snapshot.Snapshot{Version: 1, Parts: parts}) // 版本号必须严格递增

f, err := sub.Recv()                     // 阻塞收取下一个分片（Cancel/Close 可解除阻塞）
f, ok := sub.TryRecv()                   // 非阻塞收取
sub.Ack(f.Version)                       // 确认后释放旧分片
sub.Cancel()                             // 立即解除全部引用
sub.RetainedBytes()                      // 当前保留的分片字节数
```

## 关键设计：为什么队列不用 channel

每个订阅者的队列是**互斥锁保护的切片**，而非 channel。channel 会向阻塞中的接收方直接交接（handoff）分片——交接在发送时刻即生效、绕过缓冲区；若此时发布方整体驱逐队列，接收方随后可能观察到在队列中从未连续存在过的分片组合（例如先收到 v1 的片段 0，再收到 v4 的片段 3）。把"接收方出队"和"发布方驱逐+填充"放在同一把锁下，前缀不变量才能由构造保证。

## 版本内容模型核对

`snapshot/snapshot_test.go` 用确定性内容模型 `modelContent(version, index)`
独立生成每个分片的应有内容，接收侧逐分片校验：

- 数据与模型一致（任何跨版本拼接都会表现为内容不匹配）；
- 同一版本内的分片下标是从 0 开始的连续前缀；
- 版本号单调递增，中间版本只能被整体跳过，绝不回退。

覆盖场景：

- `TestPublishFasterThanConsume` — 发布快于消费，积压时跳过中间版本，最终收到最新完整版本；
- `TestCapacityPressureBackloggedSubscriber` — 1000 个版本压向不消费的订阅者，保留量恒等于一个版本；
- `TestEvictionStressKeepsPrefixInvariant` — 慢接收方反复承受驱逐，逐次校验前缀不变量；
- `TestAckReleasesOldFragments` — `Ack` 释放对应及更早版本，不触碰更新版本；
- `TestCancelReleasesImmediately` / `TestCancelUnblocksRecv` / `TestStreamClose` — 取消与关闭立即解引用并解除阻塞；
- `TestConcurrentModelVerification` — 多订阅者并发收发在 `-race` 下校验模型。

## 测试

```sh
go test -race ./...
```
