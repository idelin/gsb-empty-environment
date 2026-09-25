# policy-combiners

TypeScript 策略组合模块：deny-overrides / permit-overrides / first-applicable，
决策四值：Permit / Deny / NotApplicable / Indeterminate。Permit 携带限额（quota）
与字段遮蔽（mask）义务。

## 结构

- `src/types.ts` — 决策、义务、冲突、求值结果类型
- `src/conditions.ts` — 有限集合成员、整数比较、布尔运算（只读显式属性，缺属性抛错）
- `src/obligations.ts` — 义务合并代数：同 id 限额取 min；同字段遮蔽格式必须一致，否则冲突
- `src/combiners.ts` — 三种组合算法；义务冲突上抛为 Indeterminate，不被 Permit 掩盖
- `src/policy.ts` — 策略模块与规则定义
- `src/registry.ts` — 版本化注册表：校验通过才原子切换；模块环/悬空引用拒绝更新
- `src/engine.ts` — 求值引擎 + 决策日志；按记录版本与属性快照重放，回包旧版本属性
- `src/executor.ts` — 义务执行器：失败按逆序补偿，补偿失败保留 `compensation-failed` 可重试

## 测试

Node.js 内置测试运行器（v23+ 直接跑 TS）：

```sh
npm test   # node --test test/*.test.ts
```

## 实际运行结果（2026-09-25, Node v23.9.0）

```
ℹ tests 27
ℹ suites 8
ℹ pass 27
ℹ fail 0
ℹ duration_ms ~1400
```
