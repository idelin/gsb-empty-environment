# streamsim

在离散事件模型中按连接四元组 + 方向重建字节流。所有包、队列和链路都以整数
tick 推进，不产生任何真实网络流量。仅使用 Go 标准库。

## 模型

- `Simulator`（`sim.go`）：整数 tick 的离散事件调度器，同 tick 事件按调度先后
  FIFO 触发。
- `Link`（`link.go`）：存储转发链路，FIFO 队列 + 每包串行化 `Interval` tick +
  传播 `Delay` tick；`MaxQueue` 满时丢包计数。
- `Table` / `Conn` / `stream`（`reassembly.go`）：重组器。

## 重组语义

- **两个方向序号空间独立**：`client->server` 与 `server->client` 各自维护
  base/next。
- **连接 epoch**：SYN 携带更大 epoch 时开启同四元组的新连接；旧 epoch 的包
  计入 `StaleDrops` 丢弃，绝不混入新流。
- **重传与重叠冲突**：重叠字节相同视为重传；不同则记录 `Conflict`（保留先到
  字节，拒绝后到字节），绝不静默合并。
- **缺口不填造**：`Deliver` 遇到缺口即停；`Finalize` 以 `Gap` 块显式输出缺失
  区间，不编造任何字节。
- **半关闭**：单方向 FIN 后另一方向仍可收发；越过 FIN 的载荷字节被丢弃并计入
  `DataAfterFin`。

## 运行

```sh
go test ./...   # 全部测试
go run ./cmd/demo
```

## 运行样例（`go run ./cmd/demo` 末尾摘要）

```
=== reconstructed streams ===
conn{10.0.0.1:5000>10.0.0.2:80 epoch=0} client->server
  delivered: "hello "
  GAP[6:12] (missing, not fabricated)
  data: "end"
  CONFLICT at off=13: kept 0x6e, rejected 0x4e
  dropped 4 payload byte(s) beyond FIN
  fin=true
conn{10.0.0.1:5000>10.0.0.2:80 epoch=0} server->client
  delivered: "200 OK"
  fin=true
conn{10.0.0.1:5000>10.0.0.2:80 epoch=1} client->server
  delivered: "again"
stale epoch packets dropped: 1
```

## 测试结果（`go test ./...`）

15 个测试全部通过：顺序/乱序交付、相同重传、冲突重叠、缺口不填造、半关闭、
epoch 隔离、双方向独立序号空间、FIN 前尾部缺口、交付后重传、finalize 块顺序、
事件顺序、链路时延/串行化、队列丢包、端到端链路。

```
ok  	streamsim
```
