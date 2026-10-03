# 多模式复合事件序列匹配器（CEP Engine）

基于**非确定性有向无环图（NFA）**的复杂事件处理引擎，TypeScript 实现，
零第三方依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 能力

- **序列模式 + 否定**：识别 `A -> B` 且中间不得出现 `C` 的时序模式；
  每个模式展开为 NFA 状态链 `S0 --A--> S1 --B--> S2(final)`，`S1` 上挂否定边。
  每个 `A` 事件生成一个并发运行实例（NFA 非确定性，skip-till-any 语义）。
- **跨事件时间滑窗**：`B` 必须落在 `(A.t, A.t + windowMs]` 内（边界含端点）。
- **乱序事件补偿触发**：事件允许乱序到达；迟到的 `A`/`B` 基于事件缓冲区
  补偿触发匹配，命中通知带 `compensated: true` 标记。
- **精准撤销**：迟到的否定事件 `C` 只撤销时间区间 `(A.t, B.t)` 严格包含它的
  已发命中，并写入引擎日志。
- **模式超时丢弃**：水位线（已见最大事件时间）越过 `A.t + windowMs` 后，
  未完成的运行被丢弃并记 `run-timeout` 日志。
- **多模式**：同一事件流可驱动多个独立注册的模式。

## 运行环境

Node.js **>= 23.6**（原生 TypeScript 类型擦除 + `node:test`，无需编译与依赖）。

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 验收场景

`A@0`、`B@5000` 到达 → 产生命中 `m-1`；随后迟到事件 `C@2000` 到达 →
系统精准撤销 `m-1` 并记入日志：

```
>>> HIT    m-1 [A->B-without-C] A@0 -> B@5000
>>> REVOKE m-1 [A->B-without-C] reason: negation C@2000 falls inside (0, 5000)

[wm=    0] run-started  run#1 anchored at A@0
[wm= 5000] match        m-1: A#a1@0 -> B#b1@5000
[wm= 5000] retract      m-1 retracted: negation C#c1@2000 inside (0, 5000)
```

## 结构

- `src/engine.ts` — 引擎核心（模式注册、NFA 运行管理、滑窗、乱序补偿、撤销、超时）
- `test/engine.test.ts` — 10 个 `node:test` 用例（含验收场景、边界、乱序、多模式）
- `examples/demo.ts` — 验收场景的可运行演示
