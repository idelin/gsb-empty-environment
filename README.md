# 追溯性信用额度占用流水账（Retroactive Credit Ledger）

支持**历史冲正**与**重叠期冻结**的信用额度管理器（TypeScript 实现，零依赖）。

## 特性

- **只追加流水账**：所有变更（含冲正）均为新增记录，历史记录永不物理删除。
- **双时间轴**：业务时间（`time`/`from`/`to`）表达事件生效的历史时刻；入账顺序（`seq`/`recordedAt`）表达记账次序，支持在历史时间点追溯入账。
- **追溯性额度调整**：`adjustLimit` 可将历史期间 `[from, to)` 的额度设定为指定值（`set`）或增减临时额度（`delta`）。
- **重叠期冻结**：`freeze(from, to, amount)` 期间冻结，多笔冻结重叠时效果叠加。
- **历史冲正**：`reverse(targetId)` 追加冲正记录逻辑抵消目标事件；冲正之冲正可恢复。
- **违约检测**：每次入账后全时间轴重算，识别「占用（消费净额 + 冻结）> 有效额度」的时间点，输出违约时间点与超额差量，并写入审计日志（`ALERT`/`RESOLVED`）。

## 运行环境

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译；推荐 ≥ 23）

## 运行

```bash
npm test    # Node.js 内置测试运行器（node:test）
npm run demo  # 验收场景演示
```

## 验收场景

初始额度 1000；时间 10 消费 800；时间 20 消费 150；时间 30 追溯将时间 5 至 15 的额度调整为 700。
系统自动识别时间 10 发生 100 的超额并记录审计日志。

`npm run demo` 输出：

```
=== 验收场景：追溯性信用额度占用流水账 ===

初始额度: 1000
时间10 消费 800
时间20 消费 150
当前违约检测: [] （无违约）

时间30 追溯调整：将时间5至15的额度调整为 700

--- 违约警报 ---
⚠️  时间10 超额：占用800 > 额度700，超额差量 100

--- 审计日志 ---
[audit#1] 状态=ALERT 触发事件=evt-3 入账时间=30
    时间10: 占用800 / 额度700 / 超额100

--- 全量流水（只追加，不删除） ---
  seq=1 recordedAt=1 CONSUME id=evt-1 {"id":"evt-1","seq":1,"recordedAt":1,"type":"CONSUME","time":10,"amount":800}
  seq=2 recordedAt=2 CONSUME id=evt-2 {"id":"evt-2","seq":2,"recordedAt":2,"type":"CONSUME","time":20,"amount":150}
  seq=3 recordedAt=30 LIMIT_ADJUST id=evt-3 {"id":"evt-3","seq":3,"recordedAt":30,"type":"LIMIT_ADJUST","from":5,"to":15,"amount":700,"mode":"set"}
```

## 测试结果

`npm test`（node:test，7 个用例全部通过）：

```
✔ 验收场景：追溯调低历史额度后识别出时间10超额100并记录审计日志
✔ 重叠期冻结：同一时段多笔冻结效果叠加
✔ 临时额度：历史期间插入临时额度提升（delta）
✔ 历史冲正：冲正历史消费后违约消失，原始记录不物理删除
✔ 冲正之冲正：恢复被冲正的事件效果
✔ 冲正额度调整：追溯调整被冲正后额度恢复
✔ 释放与参数校验
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

## API 概览

| 方法 | 说明 |
| --- | --- |
| `new CreditLedger(initialLimit)` | 创建账本，设定初始额度 |
| `consume(time, amount)` | 业务时间 `time` 占用额度 |
| `release(time, amount)` | 业务时间 `time` 归还额度 |
| `freeze(from, to, amount)` | 期间 `[from, to)` 冻结，重叠叠加 |
| `adjustLimit({from, to, mode, value, recordedAt?})` | 追溯调整历史期间额度（`set`/`delta`） |
| `reverse(targetId)` | 历史冲正（逻辑抵消，不删除记录） |
| `usageAt(t)` / `limitAt(t)` | 查询时刻 `t` 的总占用 / 有效额度 |
| `detectViolations()` | 返回 `{time, usage, limit, excess}[]` |
| `events()` / `auditLog()` | 全量流水 / 审计日志（只读副本） |

区间约定：所有期间均为左闭右开 `[from, to)`；消费/释放为持续占用（阶梯函数），违约扫描在所有变化点求值。
