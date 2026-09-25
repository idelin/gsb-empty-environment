# policy-combining

纯 TypeScript 实现的策略组合模块，零运行时依赖，仅使用 Node.js 内置模块测试。

## 能力

- **组合算法**：`deny-overrides`、`permit-overrides`、`first-applicable`（`src/combiners.ts`）。
- **四值决策**：`Permit` / `Deny` / `NotApplicable` / `Indeterminate`；`Permit` 携带限额（quota）与字段遮蔽（field-mask）等义务。
- **义务合并代数**（`src/obligations.ts`）：限额按资源取更严（min）、字段遮蔽取并集、输出格式相同去重；不同输出格式无并集，产生**义务冲突**并上浮为 `Indeterminate`，绝不被允许结论掩盖。结果规范化排序，合并满足交换律/结合律/幂等律。
- **属性模型**（`src/condition.ts`、`src/attributes.ts`）：策略只读显式声明的属性；值域限整数、布尔、有限字符串集合；条件仅整数比较、集合成员/子集与布尔运算；不接任何外部身份服务。v1（平铺）/v2（作用域）属性回包统一规范化为只读视图。
- **模块注册表**（`src/registry.ts`）：版本化策略模块；`activate` 先全量校验（存在性、委托图无环）再原子切换指针，失败不留部分状态；决策追加式落日志，`replay` 用历史版本模块与原始回包重放，逐位一致。
- **义务执行器**（`src/executor.ts`）：顺序应用义务，失败时按逆序补偿；补偿失败保留 `compensation-pending` 可重试状态，`retry` 从断点恢复。

## 运行测试

需要 Node.js ≥ 23.6（原生 TypeScript 类型剥离；Node 22.6+ 可加 `--experimental-strip-types`）：

```sh
npm test
# 或
node --test test/*.test.ts
```

实际运行结果见 `RESULTS.md`。
