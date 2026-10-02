# Seed-Bootstrap Gossip (TypeScript)

基于种子节点（Seed Nodes）的新节点冷启动自发现组件：新集群组网与孤儿节点入网握手器。
节点启动时向配置的静态 Seed 列表发起 **Bootstrap 握手**，拉取初始成员快照，随后立即接入常规
Gossip 洗牌环（shuffle ring），逐步融入全网。

零依赖，仅使用 Node.js 内置模块（`net`、`node:test`）。TypeScript 源码由 Node ≥22.6+
的原生类型擦除（type stripping）直接运行。

## 架构

```
src/
  types.ts       消息协议与成员元数据类型（BOOTSTRAP_REQUEST/RESPONSE, GOSSIP_SYNC）
  transport.ts   Transport 接口 + TcpTransport（换行分隔 JSON，请求/响应，带超时）
  memory-transport.ts  InMemoryTransport：同进程传输，用于测试与沙箱环境
  membership.ts  成员表：按 heartbeat 的 last-writer-wins 合并
  gossip.ts      GossipNode：Bootstrap 握手器 + Gossip 洗牌环
test/
  membership.test.ts  成员表合并语义单元测试
  bootstrap.test.ts   验收测试：50 节点集群 + 3 孤儿节点冷启动
examples/
  demo.ts        运行样例：完整演示冷启动自发现过程
```

## 工作流程

1. **Bootstrap 握手**：新节点（孤儿节点）向静态配置的 Seed 列表并发发送
   `BOOTSTRAP_REQUEST`（携带自身元数据）。Seed 立即将新节点注册进成员表，并回以
   `BOOTSTRAP_RESPONSE`，其中包含其全量成员快照。
2. **快照合并**：新节点按 heartbeat 的 last-writer-wins 规则合并快照，一次往返即获得
   全网成员元数据。
3. **融入洗牌环**：握手完成后节点启动 Gossip 定时器，每轮对已知存活节点做
   Fisher–Yates 洗牌，取前 `fanout` 个目标做 push-pull `GOSSIP_SYNC` 交换，
   自身元数据随之在环中扩散。

## 传输层

`GossipNode` 通过 `transport` 选项注入传输实现（`Transport` 接口）：

- 生产环境默认使用 `TcpTransport`（`net` 模块，跨机器真实组网）；
- 测试与样例使用 `InMemoryTransport`（同进程 hub，消息仍经 JSON 序列化与异步投递），
  以便在禁止监听 socket 的沙箱环境中完整验证协议行为。

## 运行

需要 Node.js >= 22.6（仓库内 `.nvmrc` 指定 23.9.0）：

```bash
nvm use            # 或任意 >= 22.6 的 node
npm test           # 内置 node:test 运行全部测试
npm run demo       # 运行样例：50 节点集群 + 3 孤儿节点冷启动
```

## 验收边界

`test/bootstrap.test.ts` 覆盖：

- 50 个全新节点仅配置单点 Seed 地址启动，Gossip 收敛为全网视图；
- 3 个孤儿节点仅配置单点 Seed 地址，**1 秒内**通过 Bootstrap 拉取全部 50 个节点元数据；
- 握手完成的瞬间 Seed 即已注册孤儿节点（无需等待 Gossip 周期）；
- 孤儿节点立即融入 Gossip 洗牌环：收到环内Gossip、自身元数据扩散到 Seed 之外的节点。
