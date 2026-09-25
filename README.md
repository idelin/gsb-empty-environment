# policy-decision-cache

带属性级失效语义的策略判定缓存。TypeScript 实现，零依赖，使用 Node.js 内置
`node:test` 验证（Node ≥ 22.6，推荐 ≥ 23.6 可免标志直接运行 `.ts`）。

## 设计要点

- **缓存条目记录三要素**（`src/cache.ts` 的 `CacheEntry`）：
  - `reads`：判定过程中**实际读取**的属性及读取时刻的版本号（由 `src/evaluator.ts`
    的 `TrackedReader` 在求值时记录，短路求值未触及的属性不计入）；
  - `ruleVersion`：产生该结果的规则版本；
  - `expiresAt`：时间失效点（命中规则的 `ttlSeconds` 推导，默认拒绝兜底 60s）。
- **属性更正仅失效相关条目**：属性仓库（`src/attribute-store.ts`）为每个
  （主体, 属性）维护独立版本号并发出变更事件；缓存只删除“读取过该属性”的
  该主体条目。另有惰性兜底：命中前逐条比对版本，漏事件也能发现。
- **跨主体 / 跨上下文不串用**：缓存键 = `subjectId + contextId`，天然隔离。
- **策略能力受限**（`src/policy.ts`，加载时 `validatePolicy` 强制校验）：
  - 只能读取**显式声明**的属性（未声明属性出现在条件中直接报错，写入未声明
    属性同样被拒绝）；
  - 条件仅支持：有限集合成员判断（`in`）、整数比较（`eq/ne/lt/le/gt/ge`，
    非整数比较值被拒绝）、布尔等值，以及 `and/or/not` 布尔组合；
  - 求值为本地纯函数，属性只来自进程内 `AttributeStore`，**不接外部身份服务**。
- **失效语义**：
  - 未读取属性改变 → 不失效（该属性不在任何条目的 `reads` 中）；
  - 已读取属性删除 → 必须失效（删除保留墓碑版本并递增，事件 + 版本比对双保险）；
  - 规则版本变化 → 清空全部缓存，旧版本的允许结果不得沿用。

## 运行

```sh
npm test    # node --test（Node 23 直接执行 TypeScript）
npm run demo
```

Node 22.6–22.x 需加标志：`node --experimental-strip-types --test`。

## 结构

- `src/policy.ts` — 策略模型与校验（显式属性、有限集合、整数、布尔运算）
- `src/evaluator.ts` — 本地求值器，记录实际读取的属性及版本
- `src/attribute-store.ts` — 进程内属性仓库，逐属性版本号 + 变更事件
- `src/cache.ts` — 判定缓存：属性级失效、规则版本失效、时间失效点
- `src/pep.ts` — 策略执行点，串联求值与缓存
- `test/policy-cache.test.ts` — 16 个用例覆盖全部失效语义
- `examples/demo.ts` — 可运行样例
