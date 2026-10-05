# 导出脱敏检查器（Export Desensitization Checker）

对导出计划做静态检查：沿字段计算依赖传播敏感级别，仅当聚合满足最小分组人数时才允许降级，并为每条违规输出回溯到原始敏感字段的来源链。

## 核心规则

1. **级别沿依赖传播**：派生字段继承所有输入的最高敏感级别。因此"隐藏原字段"（不导出 source）无法消除泄露——派生字段仍携带原字段的级别。
2. **聚合降级**：`aggregate` 节点仅当**每个分组人数 ≥ minGroupSize** 时才降级到 `downgradeTo`，否则保持传播来的级别。
3. **过滤缩减人数**：`filter` 节点减少行数，可能使下游聚合不再满足最小分组人数，令原本合法的降级失效（检查器会校验过滤后人数不超过输入人数）。
4. **共享计算节点**：多个导出字段引用同一子图时，该节点只求值一次（缓存），其泄露会同时出现在多条违规来源链中。

## 计算图节点

| kind | 说明 |
|---|---|
| `source` | 原始字段，携带基础级别与总体人数 |
| `derive` | 派生计算（concat/hash/round 等），级别 = 输入最高级别 |
| `filter` | 谓词过滤，`remainingPopulation` 为过滤后人数 |
| `aggregate` | 分组聚合，`groupSizes` + `minGroupSize` + `downgradeTo` 决定降级 |

## 使用

```bash
npm run demo   # 运行演示场景，打印违规报告
npm test       # 运行测试（node:test，Node >= 23 直接执行 TS）
```

```ts
import { checkExportPlan, formatReport } from "./src/checker.ts";

const report = checkExportPlan(plan);
console.log(formatReport(report));
// report.violations[i].chains  —— 违规来源链（导出字段 -> ... -> 原始敏感字段）
// report.sharedNodes           —— 被 >=2 个导出共享的计算节点
// report.evaluationCounts      —— 每个节点求值次数（共享节点为 1）
```

## 文件

- `src/model.ts` — 敏感级别、节点与导出计划类型
- `src/checker.ts` — 传播/降级/来源链/共享节点检查逻辑
- `src/demo.ts` — 演示：派生泄露、过滤致降级失效、共享节点
- `test/checker.test.ts` — 12 个用例覆盖全部规则
