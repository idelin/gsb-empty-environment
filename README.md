# SCIM PATCH 属性越权校验

供 TypeScript SCIM PATCH 处理器调用的无依赖校验包。它只做校验，不修改资源；调用方必须在
`result.valid === true` 后才应用整批操作。

## 能力

- 按 RFC 7644 §3.5.2 解析普通属性路径、子属性路径、值过滤器
  (`emails[type eq "work"].value`) 和扩展 schema URN 路径。
- 属性名、操作符、schema URN 均大小写不敏感。
- 根据调用方提供的 schema `mutability` 逐项检查 `readOnly` / `immutable`。
- 在调用方提供的可写属性/路径集合上做前缀授权，支持过滤叶子路径。
- 无 `path` 的 `add` / `replace` 会把值对象逐层展开检查，包括 schema URN
  容器、数组元素及其子属性。
- 任一属性不存在、只读、不可变或不在可写集合内，整个 PATCH 批次均为
  `valid: false`。校验过程没有部分应用行为。
- 默认失败关闭：未知属性、未知过滤器属性、非法过滤器、非法路径都不会被放行。

## 用法

```ts
import {
  CORE_USER_SCHEMA,
  ENTERPRISE_USER_SCHEMA,
  ENTERPRISE_USER_SCHEMA_URN,
  validatePatch,
} from "./index.ts";

const result = validatePatch(patchBody, {
  schemas: [CORE_USER_SCHEMA, ENTERPRISE_USER_SCHEMA],
  writableAttributes: [
    "userName",
    "name",
    "active",
    "emails",
    `${ENTERPRISE_USER_SCHEMA_URN}:department`,
  ],
});

if (!result.valid) {
  // 整批拒绝；不要执行任何 operation，也不要把前序 operation 写入资源。
  return { status: 403, body: result.violations };
}
// 此时才可应用 patchBody.Operations。
```

处理器也可以使用抛出版本：

```ts
import { assertPatchAllowed, ScimPatchValidationError } from "./index.ts";

try {
  assertPatchAllowed(patchBody, options);
} catch (error) {
  if (error instanceof ScimPatchValidationError) {
    return { status: error.status, body: error.violations };
  }
  throw error;
}
```

`writableAttributes` 中的 `emails` 是路径前缀，允许 `emails.value` 等子属性；
`emails[type eq "work"].value` 只授权该过滤叶子。显式传入空数组表示拒绝所有属性；
省略该选项时仅按 schema mutability 判断。`allowedAttributes` / `allowedPaths` 是同一选项的别名。

## 校验示例

- `roles`、`groups`、`meta`：schema 只读或不在可写集合时拒绝；无 path 的
  `replace` 值对象中夹带这些属性同样整批拒绝。
- `active`：schema 为 `readWrite` 且在可写集合内时允许。
- `emails[type eq "work"].value`：解析过滤器与叶子路径后按 `emails.value`
  的 mutability 和授权检查；改变大小写不能绕过。
- `urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:department`：
  按扩展 schema 完整 URN 前缀解析和检查；`manager` 未被授权时拒绝。

## 测试

```sh
npm test
```
