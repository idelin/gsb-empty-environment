# 响应序列化脱敏组件

按字段敏感级别在序列化时替换值，递归覆盖嵌套对象、数组元素与对象键；对原始值、编码变体、分隔拼接和跨字段/跨数组元素拼接做泄露检测；同时把 `token -> 原始值` 的可逆映射保存在独立的审计仓库中，不混入响应体。

## 快速开始

```ts
import {
  AuditVault,
  Desensitizer,
  assertNoLeak,
  restoreWithVault,
} from "./src/index.ts";

const desensitizer = new Desensitizer({
  fields: {
    "**.phone": "restricted",
    "users[].email": "sensitive",
    profile: "internal",
  },
  nestedMode: "leaves",
});

const result = desensitizer.serializeWithAudit({
  users: [
    {
      phone: "13800138000",
      email: "alice@example.com",
      note: `call 138-0013-8000 or MTM4MDAxMzgwMDA=`,
    },
  ],
});

console.log(result.body);   // 只含 {{...}} 脱敏 token
console.log(result.audit);  // token、路径、级别、原因、原始值
assertNoLeak(result.body, ["13800138000", "alice@example.com"]);

const restored = desensitizer.deserialize(result.body);
```

`serialize()` 直接返回响应字符串；`serializeWithAudit()` 同时返回 `body`、`audit` 和 `vault`。`SerializationResult.toJSON()` 只输出 `body`，避免调用方误把审计映射写进响应。

## 字段敏感级别

规范级别为 `public`、`internal`、`sensitive`、`restricted`，也接受
`none/low/medium/high/confidential/secret/critical` 和 `0..3` 别名。默认
`public`，非 `public` 字段在输出中不会保留原值。

字段规则支持：

- 字段名：`phone`、`access_token`（大小写及 `_`/`-` 归一化后匹配）
- 点路径：`user.phone`、`**.phone`、`users[].email`、`*.note`
- 正则表达式或 `/.../` 字符串
- 按字段覆盖策略：`{ level: "restricted", strategy: "hash" }`

未显式配置时默认识别常见敏感字段名（`password`、`token`、`phone`、
`email`、`idCard` 等）。可用 `inferSensitiveFields: false` 关闭。
显式规则优先于推断，显式 `public` 可解除推断。

## 嵌套结构与数组

敏感字段的值如果是对象或数组，默认 `nestedMode: "leaves"`：保留结构，
逐个替换叶子值。`nestedMode: "subtree"` 则用一个 token 替换整个子树，
审计仓库保存完整原始子树，`restore()` 可恢复。

父级敏感级别会向子级继承，子字段不能把级别降回 `public`。

## 变体与拼接识别

脱敏值会作为后续全量扫描的敏感值集合。扫描覆盖：

- 原文、URL/表单编码、Base64/Base64URL、十六进制
- `\uXXXX` / `\xNN`、HTML 实体、JSON 转义
- 双层编码（默认深度 2）
- `138-0013-8000`、`138 0013 8000` 等分隔拼接
- 跨对象字段、跨数组元素的拼接
- 跨字段或跨元素的编码拼接（如分段的 Base64）
- NFKC 全角字符、大小写、反转文本（按需开启）

短于 `detection.minLength`（默认 4）的值只做整串精确匹配，避免误伤；
字段级敏感值无论长度都会整体替换。序列化完成后默认对输出再执行一次
验证，发现泄露会抛出 `DesensitizationLeakError`，而不是返回不安全响应。

## 审计与恢复

```ts
const result = desensitizer.serializeWithAudit(data);

for (const entry of result.audit) {
  console.log(entry.path, entry.level, entry.reason, entry.encoding);
  desensitizer.reveal(entry.token); // 原始值
}

desensitizer.restore(JSON.parse(result.body));

// 明文导出仅用于受控审计环境
const plain = result.vault.export();

// 推荐：口令加密导出 AES-256-GCM + scrypt
const encrypted = result.vault.export("audit-passphrase");
const vault = AuditVault.import(encrypted, "audit-passphrase");
restoreWithVault(JSON.parse(result.body), vault);
```

审计条目记录：

- `token`：输出中的不透明 token
- `path`：原始 JSON Pointer
- `level` / `reason`：触发级别与原因（字段、子树、正文变体、拼接、键）
- `original`：被替换的精确值，供 `restore()` 逆变换
- `sensitiveValue` / `encoding`：编码/拼接变体命中的原始敏感值和编码链

`storeOriginal: false` 可以生成不可逆 token；默认开启可逆映射。

默认策略 `tokenize` 不保留任何原文片段；`partial` 会有意保留首尾预览，
`hash` 输出摘要但仍可通过审计仓库恢复。根值本身不是字段，如果根值就是
敏感值，请使用 `knownSensitiveValues` 或把 `defaultLevel` 设为非
`public`。

审计仓库按实例累积；长驻进程应把映射导出到受控存储后调用
`resetAudit()`，或为每个响应创建独立实例，避免无界增长。

## 测试

```bash
npm test
```

测试覆盖嵌套对象、数组、子树替换、URL/Base64/Hex/Unicode/HTML、双层编码、
分隔拼接、跨字段/跨数组拼接、键名泄露、审计加密导出与恢复，并用
`assertNoLeak()` 验证输出不含原值。
