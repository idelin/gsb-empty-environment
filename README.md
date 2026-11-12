# 远端缓存构件签名校验

为前端单仓的远端缓存服务提供可挂到 `fetch` 处理器的 HMAC-SHA256 签名校验组件，
零运行时依赖，可运行于 Node / Deno / Edge Worker 等具备 Web Streams 的环境。

## 签名规范

签名标签为以下消息的 HMAC-SHA256，输出 32 字节并编码为**小写十六进制**（64 个字符）：

```
HMAC-SHA256(teamSecret, teamId || 0x00 || taskHash || 0x00 || artifactBytes)
```

- 分隔符固定为 `0x00`；`teamId` 与 `taskHash` 只允许非 NUL 的 ASCII 字符，不存在歧义。
- `taskHash` 必须是 16 位小写十六进制（`/^[0-9a-f]{16}$/`），大写、超长、含非 hex 字符一律拒绝。
- 标签通过 `x-cache-signature` 请求头携带，上传（`PUT`）与下载（`GET`）都必须携带。
- 标签缺失、长度不是 64、含大写或非 hex 字符返回 `400`；标签格式正确但 MAC 不匹配返回 `401`。
- MAC 比较先按原始 32 字节定长解析，再逐字节 OR 累加差异，为恒定时间比较。

## 路由与处理器

```
PUT /cache/artifacts/:teamId/:taskHash   上传，请求体即构件字节
GET /cache/artifacts/:teamId/:taskHash   下载
```

```ts
import { createCacheSignatureHandler } from "./index.ts";

const handler = createCacheSignatureHandler({
  secrets: new Map([
    ["team-alpha", new TextEncoder().encode(process.env.TEAM_ALPHA_SECRET)],
  ]),
  backend: {
    // 仅在校验通过后才会被调用，拿到的是已认证的完整构件
    async storeArtifact(teamId, taskHash, bytes) { /* 落盘/入对象存储 */ },
    async loadArtifact(teamId, taskHash) { /* 返回 Uint8Array 或 null */ },
  },
});

export default { fetch: handler };
```

`secrets` 支持四种形式：共享 `Uint8Array`、`Map`、普通对象、`(teamId) => Uint8Array | null`
 解析函数；未知团队返回 `401` 且不访问后端。

## 流式安全保证

- 上传时请求体以 `ReadableStream` 逐块读取，每块只送入增量 HMAC 并在内存缓冲，
  **校验通过前不会调用 `storeArtifact`（解包/落盘回调），也不会向下游转发任何字节**。
- 标签在读取请求体之前完成格式校验；格式非法时请求体不会被消费（测试中用永不产出的
  流验证处理器会立即拒绝）。
- 校验失败时 `ArtifactVerifier.finish()` 返回空 `Uint8Array`，不暴露已缓冲的构件内容。
- 下载时对存储层返回的字节**重新计算 MAC 并校验后才返回响应体**，可捕获上传后被改写、
  或被移动到其他任务哈希/团队下的构件；校验失败的响应体不含任何构件字节。

## 威胁模型与覆盖的攻击

- **换任务哈希**：MAC 覆盖 `taskHash`，把构件移到另一个哈希下时标签不匹配，拒绝。
- **跨团队重放**：MAC 覆盖 `teamId` 且每团队独立密钥，A 团队的标签无法用于 B 团队。
- **上传后篡改**：下载路径重新验签，存储层任何字节翻转都会导致 `401`。
- **伪造标签**：攻击者无密钥无法生成合法 MAC；恒定时间比较避免时序侧信道。

## 实现与测试

- `src/sha256.ts`：纯 TS 流式 SHA-256（FIPS 180-4）。
- `src/hmac.ts`：流式 HMAC-SHA256、小写 hex 编解码、恒定时间比较。
- `src/cache-signature.ts`：签名工具、`ArtifactVerifier`、`readAndVerifyArtifact`、fetch 处理器。
- `test/`：24 个用例，覆盖 FIPS/RFC 4231 已知向量、与 Web Crypto 交叉验证、换哈希、
  跨团队重放、上传后篡改、分块边界（1/2/15/16/55/63/64/65/128/255/400 字节）、
  标签各类畸形输入、以及校验前不读取/不回调。

```bash
npm test        # Node >= 22，内置 test runner，无需安装依赖
npm run typecheck  # 需本地具备 typescript（tsc）
```
