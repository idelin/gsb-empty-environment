云端 API 网关受理客户端提交的 JWE（RFC 7516）紧凑序列化加密令牌时，若解密后直接对 `zip="DEF"` 载荷无限制解压、无上限执行 `PBES2-HS256+A128KW` 的 `p2c` 迭代次数、未校验 `ECDH-ES` 临时公钥 `epk` 位于目标曲线（P-256）上（无效曲线攻击泄漏服务端静态私钥），或忽略 `crit` 关键头扩展，攻击者可用几百字节的 JWE 瞬间打爆服务内存/CPU 或窃取长期私钥。为 TypeScript 安全宿主实现可导入的零外部依赖 JWE 准入与密码边界防御校验组件（密码原语与解压使用 Node 内置 `crypto`/`zlib`）。组件从 5 段 Base64URL 紧凑串起严格执行：（1）硬校验分段数精确为 5、禁止非规范 Base64URL 填充或空白、保护头为合法严格 JSON 且无重复键；（2）策略与防 DoS 门禁：仅允许白名单 `alg`/`enc`、`PBES2` 的 `p2c` 必须落入 `[minIter, maxIter]`（如 `[10000, 100000]`）且盐值 `p2s` 长度 ≥8 字节，严格校验 `crit` 非空且不得包含标准头名或未注册扩展；（3）`ECDH-ES` 临时公钥 `epk` 必须显式校验坐标 `(x, y)` 满足 P-256 素域范围与曲线方程 `y^2 ≡ x^3 - 3x + b (mod p)` 且非无穷远点（阻断无效曲线与小阶点攻击）；（4）对 `zip="DEF"` 解压实施流式字节上限与压缩比双重硬熔断（超过 `maxPlaintextBytes` 或 `maxRatio` 立即中止，严禁先完整解压再判长度），并保证 AAD 严格绑定原始保护头字节。附 500 字节 JWE 压缩炸弹、`p2c=10^9` DoS、曲线外伪造 `epk` 点与非法 `crit` 拦截测试。

## 实现

`src/index.ts` 是可导入入口，只有 Node 内置 `crypto`、`zlib` 依赖：

```ts
import {
  decryptJwe,
  validateJwe,
  JweSecurityError,
} from "./src/index.ts";

const policy = {
  allowedAlgorithms: ["dir", "ECDH-ES", "PBES2-HS256+A128KW"],
  allowedEncryptions: ["A256GCM"],
  pbes2: { minIterations: 10_000, maxIterations: 100_000, minSaltBytes: 8 },
  registeredCriticalExtensions: ["acme-nonce"],
  maxPlaintextBytes: 256 * 1024,
  maxRatio: 80,
};

const admitted = validateJwe(token, policy);
// admitted.aad is the exact ASCII bytes of the original protected-header segment.

const result = await decryptJwe(
  token,
  { cek },                 // alg="dir"
  // { kek }               // A128KW/A192KW/A256KW
  // { pbes2Password }      // PBES2
  // { privateKey }         // RSA-OAEP / ECDH-ES, KeyObject or PEM/DER
  policy,
);

result.plaintext;          // Buffer
result.compressed;         // true when zip="DEF" was safely inflated
```

### 默认边界

- 只接受 5 段 compact JWE；每段必须是规范 Base64URL，无 `=`、空白或尾随非规范位。
- 保护头必须是 UTF-8、严格 JSON 对象，所有层级拒绝重复键。
- 默认 `alg` 白名单不含 `RSA1_5`、`none`；默认 `enc` 白名单为各项 GCM 与 CBC-HS。
- 默认 `PBES2` 迭代范围 `[10000, 100000]`，`p2s` 至少 8 字节且必须是规范 Base64URL。
- `crit` 必须是非空、无重复的字符串数组；不能包含标准 JOSE 头名，每个扩展都必须由宿主通过 `registeredCriticalExtensions` 显式注册并实际出现在保护头中。
- `ECDH-ES` 只接受 `kty="EC"`、`crv="P-256"`、32 字节 `x`/`y`，并显式验证素域范围、曲线方程和非无穷远点；`epk` 禁止携带私钥 `d`。
- `zip` 只允许 `"DEF"`。解密 AAD 和认证标签验证完成后才启动解压；`maxPlaintextBytes` 与最大压缩比在流式输出越过边界的首个数据块时立即销毁 zlib 流。
- 默认 `maxTokenBytes=1 MiB`、`maxProtectedHeaderBytes=16 KiB`、`maxPlaintextBytes=1 MiB`、`maxRatio=100`。

所有失败均抛出 `JweSecurityError`（别名 `JweValidationError`），`error.code` 是稳定字符串，例如 `JWE_PBES2_P2C_INVALID`、`JWE_ECDH_EPK_CURVE`、`JWE_CRIT_UNREGISTERED`、`JWE_DECOMPRESSION_LIMIT`、`JWE_AUTH_TAG_INVALID`。

### 说明

- Node 运行时需要 TypeScript strip-types 支持：`node --experimental-strip-types`（Node 23 已在 `npm test` 中使用）。包入口直接指向 `src/index.ts`，不发布构建产物。
- 字符串型对称密钥按 UTF-8 解释；需要原始字节时传 `Buffer`/`Uint8Array`。RSA/EC 私钥可传 `KeyObject`、PEM 或 DER `Buffer`。
- `boundedInflate` 默认兼容 RFC 1951 raw DEFLATE 与常见 RFC 1950 zlib 包装；两者均受同一组字节上限和压缩比硬熔断约束。
- 准入校验本身不接触明文；解压熔断由 `decryptJwe` 在认证成功后执行。网关若自行实现解包，也应复用 `boundedInflate` 而不是先完整解压。

### 测试

```sh
npm test
```

测试包含：约 500 字节的压缩炸弹、`p2c=10^9` 提前拒绝、曲线外/无穷远/越界 `epk`、非法 `crit`、重复 JSON 键、非规范 Base64URL，以及 AAD 原始头字节篡改。
