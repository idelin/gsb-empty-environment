# muxdemo

一条字节通道上的多路复用协议（仅标准库）：

- **帧格式**：`type(1) | streamID(4) | length(4) | payload`，类型为
  OPEN / DATA / WINDOW_UPDATE / CLOSE / RESET。
- **增量解析**：`Parser` 接受任意分块的字节输入；`ErrNeedMoreData` 表示
  缺数据（合法前缀），`*ProtocolError` 表示坏数据；声明长度在分配前
  先与 `maxPayload` 额度比较。
- **流控**：每流独立收发窗口；接收方消费后回带序号的 WINDOW_UPDATE，
  重复/乱序的增量不重复记账；超限发送即协议错误。
- **调度**：`Flush` 每轮受连接总额度 `connQuota` 约束，控制帧优先，
  DATA 在各流间轮转——单流窗口耗尽不阻塞其他流；CLOSE 延迟到本流
  已排队数据发出之后再上线路，保证流内顺序。
- **半关闭**：双向独立状态机；本端 CLOSE 后仍可接收，对端 CLOSE 后
  仍可发送；全关闭后到达的数据按协议报错。

## 运行

```sh
go test ./...   # 测试
go run ./cmd/demo  # 运行样例
```
