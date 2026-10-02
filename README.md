# causalrouter

离散消息的因果一致性路由与交付器（Go，仅标准库）。

## 设计

- `VectorClock`（`vectorclock.go`）：版本向量，支持 `Increment` / `Merge` / `HappenedBefore`。
- `Client`（`client.go`）：每个客户端维持本地版本向量；`Send` 自增本节点分量并给消息盖章，`Receive` 合并观察到消息的时钟。
- `Router`（`router.go`）：服务器端交付器。
  - **可交付判定**：消息 `m` 可交付当且仅当 `m.Clock[m.Sender] == delivered[m.Sender]+1`（发送者的下一个事件），且其余每个分量 `m.Clock[k] <= delivered[k]`（所有前置因果事件均已交付）。
  - **暂存**：不满足条件则进入 pending 队列；每次交付后反复扫描队列（`drain`），按因果顺序交付所有被解锁的消息。
  - **重复检测**：`m.Clock[m.Sender] <= delivered[m.Sender]` 时返回 `ErrDuplicate`。
  - **循环因果检测**：在暂存消息间构建等待图（边 `m -> p` 表示 `p` 是能满足 `m` 缺失前置事件的暂存消息），用 Tarjan 算法找强连通分量；大小 > 1 的分量即循环因果——由于每个事件只能由唯一消息产生，环外无法解开，属于不可达。对环中消息生成 `Evidence`（环成员、阻塞依赖、说明）并隔离（quarantine），不再参与交付。

## 运行

```sh
go test ./... -v   # 测试
go run ./cmd/demo  # 运行样例
```

## 验收场景

客户端 X(A) 发送 `(A:1)`，客户端 Y(B) 收到后发送 `(A:1, B:1)`。接收端先收到 Y 的消息时将其暂存（`pending=1, delivered=0`），待 X 的 `(A:1)` 到达后按因果顺序交付 `A:1 -> B:1`。对应测试 `TestCausalOrderAcceptance`，样例见 `cmd/demo` 场景 1。
