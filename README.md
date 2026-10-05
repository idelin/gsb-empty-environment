# config-migrator

基于规则的配置键迁移库（TypeScript）。支持键的**一拆多**、**多合一**、一一改名，
按**前置条件**与**优先级**确定性执行；可逆部分可反向迁移并显式记录损失项；
重复迁移结果稳定（幂等）；新旧键同时出现且取值矛盾时**拒绝猜测**，直接抛错。

## 核心概念

一条 `MigrationRule` 描述一组旧键到新键的转换：

```ts
interface MigrationRule {
  id: string;                                // 唯一、稳定的规则 id
  priority?: number;                         // 越大越先执行，默认 0；并列按 id 升序
  when?: (config) => boolean;                // 前置条件，不满足则跳过
  oldKeys: string[];                         // 消费的旧键
  newKeys: string[];                         // 产出的新键
  forward(oldValues): { values, losses? };   // 正向转换 + 损失项
  backward?(newValues): oldValues;           // 可逆部分（可选）
}
```

- `oldKeys.length === 1 && newKeys.length > 1` → 一拆多
- `oldKeys.length > 1 && newKeys.length === 1` → 多合一
- 也可用工厂函数 `rename` / `split` / `merge` 快速构造（见 `src/rules.ts`）

## 执行语义

`migrate(config, rules)`：

1. 深拷贝输入（绝不修改入参），按 **优先级降序、id 升序** 依次执行规则；
2. 前置条件 `when` 针对迁移中的当前配置求值，不满足则跳过；
3. 规则的 `oldKeys` 全部缺席时跳过（这是幂等性的基础）；
4. 产出新键前做冲突检查：
   - 新键不存在 → 写入；
   - 新键已存在且值**相等** → 静默消费旧键（重复迁移/部分迁移场景）；
   - 新键已存在且值**不同** → 抛 `MigrationConflictError`，拒绝猜测；
5. 旧键被删除并记为已消费，转换产生的 `losses` 统一附上 `ruleId` 收集。

`unmigrate(config, rules)` 按正向的**严格逆序**执行各规则的 `backward`：
没有 `backward` 的规则不可逆 —— 新键原样保留并记录一条损失项。
反向迁移同样做冲突检查，同样拒绝猜测。

## 输出报告

```ts
interface MigrateResult {
  config: ConfigObject;        // 迁移后的配置
  appliedRules: string[];      // 实际执行的规则 id（按执行顺序）
  provenance: ProvenanceEntry[]; // 每个新值的来源：{ key, ruleId, sources }
  losses: LossEntry[];         // 转换中丢失的信息：{ ruleId, key, reason, value? }
  unconsumed: string[];        // 未被任何规则消费、也不是任何规则目标键的旧字段
}
```

## 示例

```ts
import { migrate, rename, split, merge } from 'config-migrator';

const rules = [
  rename('rename-env', 'env', 'environment', { priority: 10 }),
  split('split-address', 'address', ['hostname', 'port'],
    (v) => { const [h, p] = String(v).split(':'); return { hostname: h, port: Number(p) }; },
    (vals) => `${vals.hostname}:${vals.port}`,          // 提供 join => 规则可逆
  ),
  merge('merge-timeout', ['timeout', 'timeoutUnit'], 'timeoutMs',
    (vals) => vals.timeout * ({ ms: 1, s: 1000 }[vals.timeoutUnit ?? 'ms'] ?? 1),
    (v) => ({ timeout: v }),                            // 只还原可逆部分，unit 记为损失
  ),
];

const result = migrate(legacyConfig, rules);
```

运行完整演示：`npm run demo`；运行测试：`npm test`。

## 目录结构

- `src/` — TypeScript 源码（交付物）
- `dist/` — 与 `src` 对应的可运行 JS。当前环境无 tsc/网络，为手工镜像生成；
  在正常环境下执行 `npm install && npm run build` 即可由 tsc 重新生成（含 `.d.ts`）
- `test/run.mjs` — 行为测试（针对 `dist`，Node 16 无 `node:test`，自带轻量 runner）
- `examples/demo.mjs` — 端到端演示：拆分、合并、损失、幂等、反向迁移
