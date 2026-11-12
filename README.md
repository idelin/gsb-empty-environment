# 服务工作线程缓存身份

离线应用通过工作线程缓存响应时，不透明响应不能被当作已验证同源内容。实现供TypeScript宿主调用的响应缓存包，保存来源、响应类型、请求凭据模式与完整性状态；读取要求匹配全部安全属性，更新不能把缺失元数据默认为可信。用结构化响应模型测试跨来源重定向、匿名与凭据请求以及版本切换。

## 结构

零依赖纯 TypeScript 包，宿主通过 `index.ts` 导入：

- `src/types.ts` — 结构化请求/响应模型（`StructuredRequest`、`CachedResponse`、`CacheEntryInput`）
- `src/origin.ts` — 来源序列化、同源判定与缓存键（去除 fragment）
- `src/integrity.ts` — SRI 元数据校验与完整性状态归一化
- `src/cache.ts` — `ResponseCache`：写入守卫、全属性匹配读取、版本切换
- `test/response-cache.test.ts` — 基于 `node:test` 的 19 个结构化测试

## 安全模型

每条缓存记录持久化四类安全属性，读取时必须全部与请求兼容：

- **来源（origin）**：重定向后取最终响应 URL 的来源；不透明与不透明重定向过滤响应保存不透明来源（`"null"`），两个不透明来源永不同源。
- **响应类型（type）**：`basic` 只能服务同源请求；跨来源读取要求 `cors`；`opaque` 仅能被同 URL 的 `no-cors` 请求复用，且不暴露状态码、头与主体；`opaqueredirect` 仅服务 `redirect: "manual"` 请求；`error` 不可缓存。
- **凭据模式（credentialsMode）**：匿名（`omit`）读取绝不命中凭据响应；`same-origin` 可复用 `omit`/`same-origin`；`include` 只命中 `include`，防止匿名缓存回答带 Cookie 请求。
- **完整性状态（integrityState）**：`verified` 必须携带合法 SRI 元数据（sha256/sha384/sha512 + base64 摘要）；缺失元数据一律归一化为 `unchecked`，调用方空口声明 `verified` 不会被信任；`mismatch` 永不命中；带 `requireIntegrity` 的请求只接受 `verified`。

版本切换（`switchVersion`）在共享存储上返回新版本视图：旧版本记录保留但全部按 `version` 原因失配，重写后才重新可用。

## 用法

```ts
import { ResponseCache } from "./index.ts";

const cache = new ResponseCache({ version: "schema-3" });

cache.put({
  request: { url: "https://app.example/app.js", credentials: "omit" },
  status: 200,
  type: "basic",
  credentialsMode: "omit",
  integrityState: "verified",
  integrityMetadata: [{ algorithm: "sha384", digest: "..." }],
  body: "...",
});

// match 返回 undefined 或 CachedResponse；inspect 额外给出失配原因
const result = cache.inspect({
  url: "https://app.example/app.js",
  credentials: "omit",
  requireIntegrity: true,
});
```

## 测试

```sh
npm test
# 等价于 node --experimental-strip-types --test "test/**/*.test.ts"
```
