# configstream

Go 实现的配置流接收端：发送方把一个修订（revision）内的多项变更切成若干
chunk 事件发送，以提交标记（commit marker）结束；接收端保证：

- **原子更新**：变更先进入暂存区，只有提交标记到达才整体应用到快照。
- **缺序号等待**：`Seq` 在会话内单调递增，出现缺口时后续 chunk 进入缓冲区
  等待，绝不部分应用；缺口补齐后按序排空。
- **旧修订一致性校验**：重发已提交的修订时，对变更内容计算 FNV-1a 摘要与
  已提交记录比对，一致则幂等忽略，冲突返回 `ErrInconsistentReplay`。
- **断连丢弃暂存**：`Disconnect()` 清空未提交暂存与乱序缓冲，序号归零，
  会话可从 `Seq=0` 重新开始。

## API

- `NewStream()` — 创建接收端。
- `Handle(Event) error` — 消费一个 chunk 事件。
- `Disconnect()` — 断连，丢弃未提交状态。
- `Snapshot() (map[string]string, uint64)` — 读取已提交快照及修订号。
- `Pending() int` — 当前等待缺序号的缓冲 chunk 数。

## 测试

```sh
go test ./...
```

`configstream_test.go` 用切块事件 + 快照模型（参考 map）交叉核对：同一修订
内混合删除与新增、乱序投递、缺序号阻塞提交、旧修订重放一致/冲突、断连后
重发，以及 30 个随机修订的模型对照测试。
