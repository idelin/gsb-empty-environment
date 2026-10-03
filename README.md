# pbufsafe — Protobuf Wire-Format 编解码 + 异常注入容错验证（纯 Go 标准库）

零外部依赖，兼容 Go 1.11+。四个组件：

| 包 | 职责 |
|---|---|
| `wire` | 纯手工 Protobuf 二进制 wire-format 解析/编码：Varint（7 位压缩）、ZigZag、Fixed32/64、Length-delimited 嵌套消息、Packed Repeated；反射编解码（`protobuf` tag）+ 反射校验引擎（`validate` tag） |
| `store` | 崩溃安全 WAL：CRC64 帧校验，断电/磁盘满时安全回滚，Open 时自愈截断到最近一致前缀；批量写入原子性（整批一个 CRC 信封） |
| `netsim` | 确定性丢包/比特翻转链路 + 停等 ARQ 可靠会话（CRC32 帧、序号去重、超时重传） |
| `testutil` | goroutine 泄漏断言 |

## 运行

```sh
export GO111MODULE=on
go test -race -count=1 ./...   # 全部测试（含竞态检测）
go run ./cmd/demo              # 端到端演示
```

## 容错设计要点

- **断电**：`SimFile` 区分易失页缓存与持久镜像，`Crash()` 丢弃未 fsync 数据；
  `FlushPartial` 模拟断电前 OS 部分回写造成的撕裂写。恢复时 CRC 扫描在第一个
  坏帧处截断，回滚到最近一致前缀。
- **磁盘满**：写失败（`syscall.ENOSPC`，允许撕裂前缀）立即 `Truncate` 回滚到
  写入前偏移，存储保持可用，释放空间后可继续追加。
- **丢包**：数据帧 40% 丢失 + 10% 比特翻转、ACK 20% 丢失的注入下，
  仍保证 exactly-once、按序、完整交付。
