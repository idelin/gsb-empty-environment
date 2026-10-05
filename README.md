# protocol-script-reducer

协议故障注入脚本缩减工具（delta debugging）。输入一份能触发断言失败的
`delay` / `packet-loss` / `restart` 脚本，自动删减为 **1-极小** 脚本：
结果仍能复现失败，且再删除任意单步都无法复现。

## 脚本格式

```json
{
  "name": "split-brain-repro",
  "nodes": ["A", "B", "C"],
  "seed": 42,
  "steps": [
    { "type": "delay", "ms": 200 },
    { "type": "packet-loss", "node": "A", "rate": 1.0 },
    { "type": "restart", "node": "B" }
  ]
}
```

## 先后依赖与合法性

步骤之间存在顺序依赖：`restart(N)` 之后必须累计经过至少 150ms
（`recoveryMs`）的 `delay`，后续步骤才能再次操作节点 N。删掉这种恢复
延迟会让脚本**不合法**。不合法的候选脚本：

- 由 `src/validate.ts` 静态判定（重放器 `src/simulator.ts` 内部也会复查）；
- 只会以 `"invalid"` 状态记入重放日志，**永远不会被当作成功缩减**。

## 缩减流程（`src/reducer.ts`）

1. **initial**：先确认原脚本确实复现失败，否则拒绝缩减（退出码 2）。
2. **ddmin**：按 2、4、8… 的分块粒度尝试删除连续块，失败复现即接受。
3. **greedy**：单步删除迭代至不动点，保证 1-极小。
4. **verify**：对结果逐单步删除再验证一遍，全部不再失败才输出。

每一次重放（包括不合法候选）都会以 JSONL 追加到重放日志
（`src/replay-log.ts`），包含序号、时间、动作、脚本长度/哈希、被删步骤
下标、状态和完整重放摘要。

## 使用

```bash
npm run build
node dist/cli.js reduce --script examples/failing-script.json --out reduced.json --log replays.jsonl
node dist/cli.js replay   --script reduced.json     # 单次重放，打印摘要
node dist/cli.js validate --script reduced.json     # 仅校验可执行性
```

示例：`examples/failing-script.json`（9 步）可缩减为 3 步，仍触发
`split-brain: concurrent leaders` 断言失败。

## 测试

```bash
npm test
```

## 接入真实环境

归约器只依赖 `ReplayHarness` 接口（`src/types.ts`）。实现
`replay(steps): ReplaySummary`，把脚本下发到真实被测系统并回报断言是否
失败即可替换内置模拟器；`status` 必须区分 `fail` / `pass` / `invalid`。
