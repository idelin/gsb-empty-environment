# error-egress

RPC 网关的错误出口包：把来自多个服务的错误原因图按调用者权限投影后对外序列化。
根错误公开并不意味着所有嵌套原因都可公开 —— 投影只由调用者可见的子图决定。

## 模型

- `ErrorNode`（`src/types.ts`）：服务身份、租户、类型 URL、原始字节、节点级
  `visibility` 与逐字段 `disclose` 披露声明；`causes` / `details` 是有序边，
  节点可共享（对象 identity 去重）也可成环。
- `Disclosure`：`public` / `tenant`（同租户可见）/ `restricted(level)`（按
  权限级别放行）。未声明的字段一律不披露（fail-closed）。
- `ViewerContext`：租户、权限级别 `clearance`、权限代次 `generation`。

## 投影与序列化

- `SchemaRegistry`（`src/registry.ts`）是版本化注册表；同一类型 URL 在不同
  版本可以有不同的字段含义。原始字节按 schema 解码（`src/wire.ts`），未知
  类型或畸形字节一律隔离：节点保留但不输出任何字段与原始字节。
- `projectErrorGraph(root, viewer, registry, budgets, key)`（`src/project.ts`）
  只遍历可见节点，因此返回的序号、原因数量、去重结果、截断位置与序列化
  字节数完全由可见子图决定，不泄漏隐藏节点的存在。
- 规范序列化（`src/canonical.ts`）为逐行 JSON 帧：`H` 头部、`N` 节点、
  `F` 字段、`C` 原因边、`D` 详情边。每个字段独占一帧，截断只发生在帧
  （字段）边界；字节与节点预算按投影后的规范序列化计量。

## 截断与继续游标

- 预算耗尽时返回 `truncated: true` 与 HMAC 签名的继续游标
  （`src/cursor.ts`）。游标绑定租户、注册表版本、权限代次与完整投影计划
  摘要，跨租户 / 注册表 / 代次或图被改动后均不可复用。
- `continueProjection(...)` 校验游标后从断点续传；各段拼接等于完整输出。

## 诊断与公开证书

- `recordDiagnostic`（`src/certificate.ts`）为后台保存完整诊断（含全部
  节点与字段）；`publicCertificate` 只含获准字节及其 SHA-256，不携带任何
  隐藏子图信息。

## 测试

```
npm test
```

`test/reference.ts` 是独立的小图投影解释器，与引擎不共享投影 / 渲染 /
预算代码；`test/history.test.ts` 做成对历史对照（含注册表热更新与游标
续传），并对全部预算组合扫描比对。覆盖场景：共享节点经两种权限路径、
隐藏分支先于公开分支、递归详情、注册表热更新、恰好只能放下空字符串的
预算，均为确定输出。
