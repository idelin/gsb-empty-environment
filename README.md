# secure-view-engine

行可见性 + 字段遮蔽的策略引擎，TypeScript 实现，零依赖（Node ≥ 22.6 原生类型擦除，无需编译）。

## 安全属性

- **固定流水线顺序**：过滤 → 遮蔽 → 排序 → 聚合。被隐藏行在第一步即被丢弃，
  不参与计数、排序、聚合与序列化，输出中不含隐藏行数量或内容。
- **固定公开 schema**：输出结构恒为 `secure-view/v1`，行仅含 schema 声明的字段（按声明顺序），
  非 schema 字段一律丢弃。
- **策略只读显式属性**：条件仅引用声明过的属性名（`row.attrs`），绝不读取行负载数据，
  不调用外部身份服务。条件仅限三类构造：有限集合成员（`in`）、整数比较
  （`eq/ne/lt/lte/gt/gte/between`，安全整数）、布尔组合（`and/or/not/const`）。
- **聚合只在可见行上进行**，且跳过被遮蔽与 null 值。
- **排序不泄漏遮蔽原值**：排序作用于遮蔽后的公开视图；遮蔽项按内容无关的固定秩
  （值 < null < MASKED）恒排最后，方向键只影响真实值之间的次序。
- **错误不显示原值**：`PolicyError` 只包含属性/字段名，绝不包含属性值或负载值；
  求值器是全函数（不抛异常），类型不符的原子条件恒为 false。
- **null 与遮蔽可区分**：遮蔽为唯一符号 `MASKED`（序列化为 `"[MASKED]"`），
  真实 null（含缺失负载字段）保持 `null`。遮蔽默认拒绝（deny by default）。

## 运行

```sh
npm run sample   # 运行样例，打印公开 JSON 视图
npm test         # node:test 内置测试运行器
```

## 文件

- `src/policy.ts` — 条件 AST、校验器、全函数求值器
- `src/engine.ts` — 安全流水线（过滤/遮蔽/排序/聚合）与公开输出 schema
- `src/sample.ts` — 运行样例
- `test/` — node:test 测试（19 个用例）
