# seedboot — 基于种子节点的新节点冷启动自发现（Go 标准库）

新集群组网与孤儿节点入网握手器：节点启动时向配置的静态 Seed 列表发起
Bootstrap 握手，拉取初始成员快照并逐步接入常规格网（Gossip 洗牌环）。

## 架构

```
            Bootstrap 握手 (POST /bootstrap)
   新节点 ───────────────────────────────► Seed（权威成员表）
   新节点 ◄─────────────────────────────── Seed 回传全量成员快照
      │
      │  Gossip 反熵交换 (POST /gossip, 双向合并, LWW)
      ▼
   洗牌环：随机打乱的 peer 环，每轮取 fanout 个目标，走完整环后重洗
```

- `discovery/seed.go`：Seed 节点，应答 Bootstrap 握手、登记新节点、参与 Gossip。
- `discovery/node.go`：常规节点，`Bootstrap()` 冷启动入网，`gossipLoop` 持续收敛。
- `discovery/ring.go`：Gossip 洗牌环，保证长期均匀覆盖全部 peer。
- `discovery/store.go`：线程安全成员表，按心跳时间做最后写入者获胜合并。
- `discovery/memnet.go`：进程内内存网络（`net.Pipe` 承载真实 HTTP 协议栈），
  供测试与单机演示使用；生产部署默认走 TCP，无需任何改动。

## 运行样例

```console
$ go run ./cmd/demo
seed listening at mem-1, holding 50 members
3 fresh nodes bootstrapped in 1.312292ms
  fresh-0 @ mem-2 knows 51 members
  fresh-1 @ mem-3 knows 52 members
  fresh-2 @ mem-4 knows 53 members
after gossip: fresh-0 knows 53 members
after gossip: fresh-1 knows 53 members
after gossip: fresh-2 knows 53 members
seed now holds 53 members
```

## 测试结果

验收边界：3 个全新节点仅配置单点 Seed 地址，1 秒内通过 Bootstrap 拉取
全网 50 个节点元数据，并立即融入 Gossip 洗牌环。

```console
$ go test -race ./... -v
=== RUN   TestBootstrapColdStart
    discovery_test.go:81: 3 nodes bootstrapped in 1.308542ms, each pulled 50 member metadatas
--- PASS: TestBootstrapColdStart (0.11s)
=== RUN   TestBootstrapSeedFailover
--- PASS: TestBootstrapSeedFailover (0.00s)
=== RUN   TestRingShuffle
--- PASS: TestRingShuffle (0.00s)
PASS
ok      seedboot/discovery
```

- `TestBootstrapColdStart`：验收主用例 —— 3 节点 1.3ms 完成 Bootstrap，
  各自拉取 50 个成员元数据，并经 Gossip 互相发现、Seed 反向学到全部新节点。
- `TestBootstrapSeedFailover`：孤儿节点入网 —— 首个 Seed 不可达时自动切换后续 Seed。
- `TestRingShuffle`：洗牌环走完整环前不重复选取目标，耗尽后自动重洗。
