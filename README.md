# pseudonymizer

对 JSON 兼容树中的指定字段生成**稳定假名**的 TypeScript 库，零依赖，仅需 Node.js ≥ 23.6（原生运行 TypeScript）。

## 特性

- **稳定假名**：`HMAC-SHA256(secret, domain ‖ canonical(value))` —— 同域同值同假名，跨多次运行、多个引擎实例保持一致（引用一致性）。
- **域隔离**：域（domain）参与 HMAC 输入，同一值在不同域得到不同假名。
- **外键可关联**：`users[].id` 与 `orders[].userId` 配置同一域即可保持 join。
- **碰撞可检测**：映射表按 `domain → pseudonym` 索引并保存完整 `valueHash`；同一假名对应不同原值时产生 `COLLISION` 错误（含具体路径），该值不会以他人假名输出。
- **映射与脱敏数据分离**：`pseudonymize()` 返回的 `data` 不含任何原值；原值只存在于 `exportMapping()` 的独立导出中（须另行加密保管）。
- **未授权字段不透传**：未被任何规则命中的叶子值一律替换为 `[REDACTED]`；`unauthorizedPolicy: "error"` 时同时记录 `UNAUTHORIZED_FIELD` 错误。显式 `allow` 规则才可原样透传。
- **路径可定位**：所有错误/日志携带具体路径（如 `$.users[0].email`）；对象键无序（规范化时排序）、数组有序。
- **嵌套数组**：`[]` 匹配任意数组下标，`*` 匹配任意对象键，任意深度嵌套均可处理。
- **日志安全**：日志条目只含路径、域名、假名，绝不含原值（有测试保证）。

## 用法

```ts
import { Pseudonymizer } from "./src/index.ts";

const engine = new Pseudonymizer({
  secret: process.env.SECRET!,            // 轮换密钥即轮换全部假名
  rules: [
    { pattern: "users[].email", action: "pseudonymize", domain: "pii.email" },
    { pattern: "users[].id",    action: "pseudonymize", domain: "user.id" },
    { pattern: "orders[].userId", action: "pseudonymize", domain: "user.id" }, // 外键同域
    { pattern: "users[].country", action: "allow" },                          // 显式授权透传
  ],
  unauthorizedPolicy: "error",            // 未授权字段：报错 + 脱敏（默认 "redact"：仅脱敏）
  logger: (e) => console.log(e),          // 日志不含原值
});

const { data, issues, stats } = engine.pseudonymize(input); // data 可安全导出
const mapping = engine.exportMapping();                     // 含原值，单独加密保管

// 跨运行保持一致：把 mapping 传给新实例
const engine2 = new Pseudonymizer({ secret, rules, mapping });
```

### 路径模式

| 片段 | 含义 |
| --- | --- |
| `name` | 对象键 |
| `*` | 任意单个对象键 |
| `[]` | 任意数组下标 |

### 错误码

`COLLISION`（假名碰撞）、`UNAUTHORIZED_FIELD`（未授权字段）、`NON_JSON_VALUE`（undefined/函数/循环引用/NaN 等）、`INVALID_RULE`（规则非法，构造时抛出）。均携带 `path`。

## 运行

```sh
npm test    # node --test（Node 内置测试运行器）
npm run demo
```

> 本环境无网络，`typescript` 未安装；仓库附带 `tsconfig.json`（`erasableSyntaxOnly`），有网后 `npm i -D typescript && npx tsc` 可做完整类型检查。
