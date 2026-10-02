# Chandy-Lamport 一致性快照记录器

Go 标准库实现的分布式全局一致性快照截取算法（Chandy-Lamport）。

## 原理

- 集群中各进程通过**有向 FIFO 信道**持续进行转账（应用消息）。
- 任一进程可发起快照：先**记录本地状态**，随即向所有出信道发送 **Marker**，并开始记录所有入信道上的在途消息。
- 进程首次在某信道收到 Marker 时执行同样动作（记录本地状态、向下游转发 Marker），并停止对该信道的记录；之后每收到一个 Marker 就关闭对应入信道的记录。
- 当某进程的所有入信道都收到 Marker 后，输出：`本地状态 + 各入信道在途消息集合`。
- 全程**无需暂停业务**，得到的是一个一致割（consistent cut）。

## 结构

- `snapshot.go` — 算法实现：`Process`（单 goroutine 事件循环，无锁）、`Message`（Transfer/Marker）、`NewCluster`（全连接 FIFO 信道）、`SnapshotResult`。
- `snapshot_test.go` — go test 验收：3 节点并发转账中由 A 发起快照，校验总资产严格守恒。
- `cmd/demo/main.go` — 可运行的演示样例。

## 运行

```sh
go test -v -race ./...   # 测试
go run ./cmd/demo        # 演示
```

## 运行样例（go run ./cmd/demo）

```
initial balances: A=100 B=200 C=300 (total=600)
concurrent transfers running...
node A initiates snapshot (markers flowing on all channels)
node A: local balance=163, in-flight=1 msg(s) totaling 1
  channel B->A: in-flight transfer amount=1
node B: local balance=160, in-flight=0 msg(s) totaling 0
node C: local balance=267, in-flight=1 msg(s) totaling 9
  channel A->C: in-flight transfer amount=9
snapshot total (local states + in-flight) = 600, initial total = 600
OK: total money strictly conserved across the consistent cut
```

## 测试结果（go test -v -race ./...）

```
=== RUN   TestSnapshotConservesTotal
    round 0: total=600 conserved (A=149+0, B=200+0, C=251+0 in-flight)
    round 1: total=600 conserved (A=61+0, B=238+0, C=301+0 in-flight)
    round 2: total=600 conserved (A=187+0, B=188+0, C=220+5 in-flight)
    ...（共 10 轮，均守恒）
--- PASS: TestSnapshotConservesTotal (1.58s)
=== RUN   TestSnapshotRecordsInFlightMessages
    recorded 1 in-flight transfers across channels
--- PASS: TestSnapshotRecordsInFlightMessages (0.15s)
PASS
ok      chandylamport   3.153s
```
