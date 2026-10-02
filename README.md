# Chandy-Lamport 一致性快照（Go 标准库实现）

分布式全局一致性快照截取算法：任一进程发起 Marker 标记；各进程首次收到
Marker 时记录本地状态，并向所有出边泛洪 Marker；在每条入边收到 Marker 之前，
记录该边上的在途消息。全程无需暂停业务即可捕获一致割（consistent cut）。

## 结构

- `snapshot.go` — 核心库：`Process`、`Message`（Transfer/Marker）、`Snapshot`、
  `Connect`（直连 FIFO 链路）与 `ConnectDelay`（带随机网络延迟的 FIFO 链路）。
- `cmd/demo/main.go` — 运行样例：3 节点集群持续并发转账，节点 A 发起快照。
- `snapshot_test.go` — `go test` 验收测试。

## 算法要点

- 每条链路是单个 Go channel（或单个转发 goroutine），保证 FIFO 投递——
  这是 Chandy-Lamport 正确性的前提。
- 进程收到某入边的 Marker 后：若尚未记录，先记录本地余额并向所有出边
  发送 Marker；随后把该入边标记为已见 Marker。
- 记录本地状态之后、某入边 Marker 到达之前，在该入边上收到的转账消息
  即为"在途消息"，计入该通道状态。
- 所有入边 Marker 到齐后，该进程的快照完成并输出。

## 运行

```sh
go test ./...      # 验收测试
go run ./cmd/demo  # 运行样例
```

## 验收边界

3 节点集群在持续并发转账中，节点 A 发起快照，Marker 沿信道流动；最终各
节点输出的本地状态 + 在途消息集合之和严格等于初始总资产（资金守恒）。

## 样例输出

```
=== node A (id 0) initiates Chandy-Lamport snapshot ===
node 0: local balance=98  in-flight[node 1->node 0]=1  in-flight[node 2->node 0]=1  => captured=100
node 1: local balance=96  in-flight[node 2->node 1]=1  => captured=97
node 2: local balance=102  in-flight[node 1->node 2]=1  => captured=103
captured total (local + in-flight): 300
initial total:                      300
RESULT: OK — consistent cut, assets strictly conserved
```

## 测试结果

```
=== RUN   TestSnapshotConservation   # 3 节点并发转账中快照，20 轮资金严格守恒
--- PASS: TestSnapshotConservation
=== RUN   TestSnapshotTerminates     # 任意节点发起，Marker 泛洪后全网完成
--- PASS: TestSnapshotTerminates
=== RUN   TestNoMessageLoss          # 快照后排空网络，无消息丢失/重复
--- PASS: TestNoMessageLoss
=== RUN   TestTransferRespectsFunds  # 余额不足时转账被拒绝
--- PASS: TestTransferRespectsFunds
PASS
ok  	chandy
```
