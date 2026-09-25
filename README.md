# multi-dim-quota

多维配额管理器（纯 TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 核心语义

- **多维同时满足才扣减**：一次 `acquire` 可携带多个扣减腿（leg），横跨多个账户、多个维度。任一维度额度不足即抛出 `InsufficientQuotaError`，所有维度一律不扣。用户额度足但资源额度不足时，两者均不扣。
- **幂等操作 ID**：每个操作携带 `operationId`。同一 ID + 相同请求重试只扣一次，返回原许可（permit）；同一 ID + 不同请求抛出 `IdempotencyConflictError`。已回滚的 ID 重试不会再次扣减。
- **取消与失败回滚所有维度**：`cancel(id)` / `fail(id)` 将该操作扣减的全部维度恢复原值；回滚本身幂等。已提交（committed）的操作禁止回滚。
- **策略版本化，禁止旧许可提交**：`updatePolicy` 生成递增版本号；许可绑定签发时的策略版本。策略变更后，用旧版本许可 `commit` 会抛出 `StalePermitError`，且对应预留被整体回滚。
- **策略只读显式属性**：策略只对请求中显式携带的 `attributes`（字符串 / 布尔 / 有限数值，冻结只读）求值，纯数据、纯函数，不访问任何外部身份服务。规则仅支持三种原子条件与布尔组合：
  - `in`：有限集合成员（集合元素须为有限原始值，拒绝 `NaN`/`Infinity`）
  - `int`：整数比较（`eq/lt/lte/gt/gte`，值与属性均须为安全整数）
  - `and` / `or` / `not`：布尔运算（限制嵌套深度）

## 生命周期

```
acquire ──reserved──> commit ──committed（终态，不可回滚）
   │                    │
   │                    └─ 旧版本许可 → StalePermitError + 全维度回滚
   ├──cancel/fail──> rolled-back（终态，幂等）
   └──额度不足/策略拒绝 → 无任何状态变更
```

## 运行

需要 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译步骤与任何依赖）。

```sh
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## 代码结构

- `src/policy.ts` — 规则 AST、校验器（有限集合 / 整数条件 / 布尔运算）、纯函数求值器、策略深冻结
- `src/quota.ts` — 账户与维度的额度存储：检查、扣减、恢复
- `src/manager.ts` — `QuotaManager`：acquire / commit / cancel / fail、幂等记录、版本化策略
- `src/errors.ts` — 结构化错误类型
- `test/` — `node:test` 测试（23 个用例）
- `examples/demo.ts` — 端到端运行样例
