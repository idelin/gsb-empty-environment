# 有限状态协议兼容检查器

检查**新版协议**（A 侧）能否与**旧版对端**（B 侧）配合完成全部规定会话。
双方建模为通信有限状态机（CFSM），动作分三类：

- `send`：发送消息（`!m`）
- `recv`：接收消息（`?m`）
- `tau`：不产生消息的内部动作（`τ`）

两条方向相反的 FIFO 队列连接两侧，**队列容量默认限定为 2**（可用 `--capacity` 调整），
发送在队列满时阻塞。检查器对全局状态空间做 BFS，因此报告的问题轨迹保证**最短**。

## 检查内容

| 结果 | 含义 |
| --- | --- |
| 兼容 | 无死锁、无意外接收，且所有可达状态都能到达正常终止 |
| 死锁-双方都在等待 | 双方都在等对方的消息，队列均为空（区别于正常终止） |
| 死锁-队列满 | 发送被满队列阻塞导致的死锁 |
| 意外接收 | 队首消息在对端当前状态（含 τ 闭包）不可接收 |
| 活锁 | 会话陷入循环，无法到达任何正常终止状态 |

**正常终止** = 双方都处于终止状态且两条队列均为空。

## 运行

需要 Node.js ≥ 23.6（直接运行 TypeScript，无需编译）：

```sh
node src/cli.ts <新版协议.json> <旧版对端.json> [--capacity 2]
npm test
```

退出码：兼容 `0`，不兼容 `1`，用法/规格错误 `2`。

## 协议描述格式（JSON）

```json
{
  "name": "my-protocol",
  "initial": "s0",
  "finals": ["s4"],
  "transitions": [
    { "from": "s0", "to": "s1", "send": "hello" },
    { "from": "s1", "to": "s2", "recv": "ack" },
    { "from": "s2", "to": "s2", "tau": true }
  ]
}
```

每条迁移必须且只能包含 `send` / `recv` / `tau` 之一。

## 示例

- `examples/old-peer.json` — 旧版对端（hello/ack 握手，data/done 循环，quit 结束）
- `examples/new-ok.json` — 兼容的新版
- `examples/new-deadlock.json` — 握手后双方互相等待（死锁）
- `examples/new-unexpected.json` — 发送旧版无法接收的 `ping`（意外接收）

```sh
node src/cli.ts examples/new-ok.json examples/old-peer.json
```

## 代码结构

- `src/model.ts` — 协议模型、JSON 校验、τ 闭包
- `src/checker.ts` — CFSM 组合、BFS 探索、死锁分类、活锁检测（反向可达性）
- `src/cli.ts` — 命令行入口与轨迹格式化
- `test/checker.test.ts` — 9 个场景测试（node:test）
