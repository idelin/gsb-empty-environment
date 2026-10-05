# protocol-script-shrinker

接收一个能触发协议断言失败的延迟 / 丢包 / 重启脚本，自动删减为**仍复现该失败**的最小脚本。

## 保证

- **仍复现失败**：缩减结果在确定性协议模拟器上重放仍触发同一条断言。
- **保持可执行**：脚本步骤支持 `dependsOn` 先后依赖；任何导致依赖悬空、引用未知节点或参数非法的删除都不会被接受。
- **1-极小（1-minimal）**：缩减结束时做一次完整验证轮——对结果脚本逐一删除每个步骤，凡是仍合法的脚本都不再失败，否则报错退出。
- **不合法 ≠ 成功**：不合法候选只记录、不参与重放（`outcome: "not-replayed"`），永远不会被当作成功缩减。
- **每次重放都有摘要**：所有重放（初始、候选、验证、最终）以 JSONL 追加写入日志，含脚本哈希、删除的步骤、合法性、结果与断言内容。

## 运行环境

Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译；代码只使用可擦除语法）。
如需 `tsc` 类型检查：`npm i -D typescript @types/node && npx tsc --noEmit`。

## 用法

```sh
node src/cli.ts shrink <script.json> [--out <shrunk.json>] [--log <replays.jsonl>]
node src/cli.ts replay <script.json>
npm test
```

退出码：`0` 成功；`2` 输入脚本不合法；`3` 输入脚本本身不触发失败（无法缩减）。

## 脚本格式

```json
{
  "nodes": ["A", "B"],
  "steps": [
    { "id": "s1", "type": "delay", "ms": 3 },
    { "id": "s2", "type": "restart", "node": "A", "dependsOn": ["s1"] },
    { "id": "s3", "type": "drop", "from": "A", "to": "B", "count": 1 }
  ]
}
```

- `dependsOn` 必须指向**之前出现**的步骤，构成执行依赖。
- 校验规则：步骤 id 唯一；`delay.ms` / `drop.count` 为正整数；`drop.from`/`drop.to`/`restart.node` 必须是已声明节点且 `from != to`。

## 模拟器语义（oracle）

每个 `delay` 步骤开始时，每个节点向所有对端发送心跳，心跳在 5 个时间单位后送达，并携带发送时接收方的 epoch。断言：**节点不得交付发往旧 epoch 的心跳**（即消息跨重启在途）。`restart` 使节点 epoch +1；`drop` 按发送顺序丢弃最多 `count` 条匹配的在途消息。重放在首次断言违反处停止，结果确定可复现。

## 重放摘要（JSONL）

每行一条：`seq`、`timestamp`、`kind`（`initial`/`candidate`/`verify`/`final`）、`scriptHash`、`stepCount`、`removedStepId`、`valid`、`validationErrors`、`outcome`（`failed`/`passed`/`not-replayed`）、`assertion`、`failedAtStepId`、`accepted`。

## 示例

```sh
$ node src/cli.ts shrink examples/failing-script.json
shrunk 6 -> 4 steps (17 replays logged to examples/failing-script.replays.jsonl)
```

示例中 `noise-drop`、`trailing-drop` 被删除；`warmup` 因 `queue` 依赖它而保留（删除它不合法，不计入缩减）。
