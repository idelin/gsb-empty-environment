# Piggyback Gossip 成员变动分发器（Go）

SWIM 风格的高带宽利用率状态传播：节点加入（Join）、离开（Leave）、
疑似（Suspect）等成员变动事件不单独发包，而是按**传播计数队列**管理，
捎带在常规 Ping/Ack 网络包的头部传输，并限制最大扩散轮数。

## 设计

- `gossip.Disseminator`：传播计数队列。每次组包时优先挑选传播次数最少
  的事件（`Piggyback(k)`），每捎带一次计数 +1，达到
  `ceil(λ·log2(N))`（λ=3）轮后从队列中淘汰，限制带宽占用。
- `gossip.Node`：维护成员视图表（id → 状态/incarnation）。`Ping` 携带
  事件发往对端，对端在 `Ack` 中回捎自己的队列；收到新事件时更新视图
  并重新入队继续扩散（传染模型）。旧 incarnation 的事件被丢弃。
- `gossip.Cluster`：内存模拟网络，每个周期（period）每个节点随机
  Ping 一个对等节点，Ping/Ack 双向都携带事件。

## 运行样例

```sh
go run ./cmd/demo
```

输出（100 节点 + 1 新节点，新节点 Join 后逐周期感知数）：

```
cluster: 100 nodes + newcomer "node-new"
dissemination cap: 20 rounds (lambda*log2(N))
period   aware        bar
1        7            ##
2        27           ##########
3        69           ###########################
4        100          #######################################
5        101          ########################################
converged after 5 periods (log2(100) = 6.6)
```

## 测试

```sh
go test ./... -v
```

覆盖：传播计数优先级、最大轮数淘汰、100 节点 Join 在 O(log N) 周期内
100% 收敛（验收边界）、Leave/Suspect 传播、过期事件丢弃。
