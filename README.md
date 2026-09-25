# domain-pseudonymizer

对 JSON 兼容树按字段规则生成**稳定假名**的脱敏库：同域同值同假名、跨域隔离、
碰撞可检测，映射表与脱敏数据分离导出，未被规则覆盖的字段绝不原样透传。

## 特性

- **稳定假名**：`pseudonym = "ps_" + HMAC-SHA256(key, "v1\0" + domain + "\0" + canonical(value))[0:n]`
  - 同域同值 → 同假名（跨字段、跨数组元素、跨多次运行均稳定）
  - 不同域同值 → 不同假名（域隔离）
- **引用一致性**：外键字段与主键字段配置同一 `domain` 即可继续关联（如
  `users[].id` 与 `orders[].userId` 共用 `user-id` 域）
- **规范化哈希**：对象键无序（递归排序后哈希），数组有序（顺序参与哈希）
- **碰撞可检测**：假名截断后若不同值产生同一假名，记录 `COLLISION` 错误并定位路径
- **映射分离**：`result.mapping` 与 `result.data` 分离导出；映射只存
  `pseudonym` + `valueDigest`（HMAC），不含任何原值
- **不透传**：未匹配任何规则的叶子字段一律替换为 `"[REDACTED]"` 并报
  `UNAUTHORIZED_FIELD` 错误（含 `$.path` 定位）
- **日志安全**：错误消息、映射、统计中均不含原始值

## 要求

Node.js ≥ 22.6（利用内置 type stripping 直接运行 TypeScript，零依赖）。

## 使用

```ts
import { anonymize } from "./src/index.ts";

const result = anonymize(input, {
  key: process.env.PSEUDONYM_KEY!,
  rules: [
    { path: "users.*.id", domain: "user-id" },                 // 默认 pseudonymize
    { path: "users.*.contacts.*.email", domain: "email" },     // 数组嵌套通配
    { path: "orders.*.userId", domain: "user-id" },            // 与主键同域 → 外键可关联
    { path: "orders.*.total", domain: "amount", action: "redact" },
    { path: "users.*.tmp", domain: "tmp", action: "drop" },
  ],
});

result.data;     // 脱敏后的数据（可导出）
result.mapping;  // 映射表（单独存储：domain → pseudonym/valueDigest/occurrences）
result.errors;   // [{ code, path, message }]，path 形如 $.users[0].ssn
result.stats;    // { pseudonymized, redacted, dropped, collisions }
```

规则路径语法：`a.b.c`、`users.*.email`、`users[].email`、`a.b[0].c`、`$`（根）。
`*` 同时匹配对象键与数组下标。规则可匹配容器节点（整个对象/数组按规范化 JSON 哈希）。

动作：`pseudonymize`（默认）| `redact`（替换为 `[REDACTED]`）| `drop`
（对象键删除；数组元素替换为 `[REDACTED]` 以保持顺序）。

## 运行

```sh
npm test        # Node.js 内置测试运行器（node:test）
npm run sample  # 运行 examples/run-sample.ts 样例
```

## 结构

- `src/anonymizer.ts` — 树遍历、规则匹配、未授权字段处理
- `src/pseudonym.ts` — HMAC 假名/摘要生成（域隔离）
- `src/mapping.ts` — 映射表、碰撞检测、分离导出
- `src/canonicalize.ts` — 键序无关的规范化序列化
- `src/paths.ts` — 规则路径解析/匹配、`$.path` 格式化
- `test/` — node:test 测试（19 个用例）
- `examples/run-sample.ts` — 端到端样例
