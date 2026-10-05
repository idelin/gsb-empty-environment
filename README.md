# acx — 带上下文标签的二进制事件序列算术编码

自定义整数算术编码格式（ACX1），仅用 Go 标准库。格式细节见 [FORMAT.md](FORMAT.md)。

## 用法

```go
enc, _ := acx.NewEncoder(4)          // 4 个上下文
enc.Put(0, 1)                        // (标签, 位)
enc.Put(2, 0)
snap, _ := enc.Snapshot()            // 可落盘暂停
enc2, _ := acx.Resume(snap)          // 恢复后续编，结果与一次编码逐字节一致
enc2.Put(1, 1)
frame := enc2.Frame()                // 完整帧

events, err := acx.Decode(frame, 256)
```

## 测试

```sh
go test ./...        # 全部测试
go test -v .         # 查看真实压缩率报告与有理区间核对日志
```

覆盖：交替上下文、极偏分布、4096 重标定边界、空输入、截断/非法上下文/长度超限/校验和拒绝、
快照恢复逐字节一致、big.Rat 高精度有理区间参考核对、20 万事件长序列往返。
