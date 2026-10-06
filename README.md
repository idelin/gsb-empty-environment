# tag-narrowing-analyzer

小语言类型系统与静态分析器：类型含**字面值、（封闭）对象、联合、never**，
分析器按 `tag === 字面值` 等值条件收窄分支，未知字段访问报错，分支合流取联合，
不可达分支单独标记；并以**有限对象值集合**实际执行条件，核对静态剩余类型是否精确。

> 环境无 tsc 且离线，实现采用 ESM JavaScript + JSDoc 类型注解（Node ≥ 14 可运行）。

## 模块

- `src/types.js` — 类型构造与规范化联合（拍平/去重/空联合坍缩为 never）、结构化相等、打印。
- `src/values.js` — `typeOfValue`：运行值的精确类型（原始值→字面值，对象→封闭对象类型）。
- `src/analyzer.js`
  - `getField(type, field, diags)`：字段访问；任一联合成员缺字段即报 `unknown-field` 诊断。
  - `narrowEq(type, field, lit)`：双侧收窄。字段类型为字面值联合时对象在两侧分裂；
    **缺失 tag 的成员**与运行语义一致——等值不成立，仅落入 else。
  - `join(...types)`：分支合流取联合。
  - `analyzeIf(type, {field, equals})`：完整 if 分析；剩余类型为 never 的分支标记
    `reachable: false`（不可达分支单独标记），并给出合流类型与诊断。
- `src/runtime.js`
  - `evalCondition(values, field, lit)`：在有限值集上执行条件（缺字段 → `undefined`，不成立）。
  - `checkPrecision(values, field, lit)`：静态收窄结果 vs 运行值导出类型，逐分支核对精确性。

## 运行测试

```sh
npm test   # node test/run.js
```

覆盖：联合规范化、字段访问报错、tag 收窄（含字面值联合分裂、缺失 tag）、
合流还原、双侧不可达标记、未知字段诊断，以及有限值集上的精度核对（含缺失 tag 与
无匹配字面值 → then 精确为 never 的场景）。
