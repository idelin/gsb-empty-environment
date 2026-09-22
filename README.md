# framepack

把多个待发消息打包进最大帧长度的 Go 实现，仅使用标准库。

## 设计

- **帧格式**：16 字节定长头部（magic、版本、flags、msgID、优先级、payloadLen、totalLen）+ 载荷。
  头部开销计入帧上限，每帧载荷容量为 `maxFrame - HeaderSize`。
- **分片**：消息按容量切片，首片带 `START`、末片带 `END`；剩余字节刚好装满一帧时
  该帧即带 `END`，不会多发一个空帧。
- **调度**：按优先级类严格优先，但每类每轮只有 quantum 字节的公平额度，额度耗尽
  即让位给下一类；所有类耗尽后开启新一轮。高优先级大消息最多压制小消息一轮，
  不会永久饿死。
- **取消**：`Cancel` 丢弃未发剩余部分并释放该类的队列额度；已发出的分片不可撤销。
- **解析**：`Parser.Feed` 接受任意分块的字节流；`ErrNeedMoreData`（数据不足，可继续喂）
  与 `ErrBadData`（magic/版本/flags 非法或长度超额，永久损坏）严格区分。
  所有长度额度（maxFrame、maxMsg）在任何分配之前检查。

## 运行

```sh
go test ./...    # 测试
go run ./cmd/demo  # 运行样例
```
