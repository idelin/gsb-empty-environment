# Graceful Leave 与平静期广播（TypeScript）

节点主动下线协议：节点发起 `Leave` 并广播离开意向（`LeaveIntent`），集群将其置为
`Leaving` 状态，在平静期（Quiescent Period）内阻断新流量、排空在途请求，定时器超时后
物理销毁路由条目——全程无突发 RPC 超时。

## 状态机

```
Ready --发起/收到 LeaveIntent--> Leaving --平静期超时--> 物理移除路由条目
```

- `Ready`：正常承接新流量。
- `Leaving`：拒绝新流量（`NodeDrainingError` 快速失败），在途请求继续完成。
- 超时后：路由条目从各节点路由表中删除，再访问报 `UnknownNodeError`。

## 环境要求

Node.js >= 23.6（利用原生 TypeScript 类型擦除，无需编译步骤）。

## 运行

```bash
npm test      # Node.js 内置测试运行器
npm run demo  # 运行样例：三节点集群中 B 优雅离开全过程
```

## 结构

- `src/types.ts` — 状态、`LeaveIntent` 消息、路由条目类型定义
- `src/node.ts` — `ClusterNode`：路由表、离开广播、平静期定时器、RPC 收发
- `src/errors.ts` — `NodeDrainingError` / `NoReadyNodeError` / `UnknownNodeError`
- `src/demo.ts` — 运行样例
- `test/graceful-leave.test.ts` — 7 个验收测试

## 测试结果

```
✔ node initiates Leave: local state becomes Leaving and intent is broadcast
✔ peers smoothly evict the leaving node from the ready route pool
✔ new traffic to a Leaving node fails fast during quiescent period
✔ in-flight requests drain and complete during quiescent period
✔ route entry is physically destroyed after quiescent timer expires
✔ duplicate LeaveIntent is idempotent and does not reset the quiescent timer
✔ route pool exhaustion throws NoReadyNodeError
ℹ pass 7  ℹ fail 0
```
