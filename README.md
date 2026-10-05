# 有限状态协议兼容检查器

检查**新版协议**能否与**旧版对端**配合完成全部规定会话。两版协议各自描述为
一台通信有限状态机（CFSM），动作分三类：

| 标签 | 含义 |
| --- | --- |
| `!msg` | 发送消息 |
| `?msg` | 接收消息 |
| `tau` / `τ` | 内部动作（不产生消息） |

## 组合语义

- 新版（A）与旧版（B）通过两条 **FIFO 队列**（A→B、B→A）异步通信，
  **队列容量默认为 2**（可用 `--capacity` 调整）。
- 发送 `!m`：仅当本方出队未满时可执行，消息追加到队尾。
- 接收 `?m`：仅当本方入队队首恰好是 `m` 时可执行，弹出队首。
- 内部动作 `τ`：总是可执行。
- BFS 探索全部可达全局状态，因此报告的错误轨迹是**最短**的。

## 判定结果

- **兼容（compatible）**：所有会话都能正常终止 —— 双方都到达终止状态
  （`finals`）且两条队列均已清空，并且每个可达全局状态都能到达某个
  正常终止点。
- **意外接收（unexpected reception）**：某方入队队首消息在其当前状态没有
  任何接收迁移可消费（FIFO 队首阻塞，后续消息也无法被接收）。
- **死锁（deadlock）**：没有任何可执行动作且未正常终止，并区分：
  - `both-waiting`：双方都在等待对方发送消息；
  - `peer-terminated`：一方已到达终止状态，另一方仍在空等；
  - `stuck`：双方都无法继续且未到达终止状态。
- **活锁（non-terminating）**：存在可达全局状态，从它出发永远无法到达
  正常终止（报告到达该状态的最短轨迹及一个可无限重复的循环）。

## 运行

需要 [Bun](https://bun.sh)（直接执行 TypeScript）：

```sh
bun src/main.ts                                   # 运行 4 个内置演示场景
bun src/main.ts examples/new-client-v2.json examples/old-server-v1.json
bun src/main.ts <new.json> <old.json> --capacity 3
bun test                                          # 运行单元测试
```

文件模式下，不兼容时进程退出码为 1。

## 协议 JSON 格式

```json
{
  "name": "old",
  "initial": "Idle",
  "finals": ["Done"],
  "transitions": [
    { "from": "Idle", "label": "?login", "to": "Ready" },
    { "from": "Ready", "label": "!welcome", "to": "Serving" }
  ]
}
```

`states` 无需显式列出，由 `initial` / `finals` / 迁移端点自动推导。

## 代码结构

- `src/protocol.ts` —— 协议模型、JSON 解析与校验
- `src/checker.ts` —— 积状态空间 BFS、四类判定、最短轨迹与活锁循环提取
- `src/format.ts` —— 判定结果与轨迹的可读化输出
- `src/examples.ts` —— 内置演示场景（兼容 / 双向等待 / 意外接收 / 活锁）
- `src/main.ts` —— CLI 入口
- `test/checker.test.ts` —— 单元测试
