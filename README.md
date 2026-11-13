# JWE 头准入包（jwe-header-admission）

面向 TypeScript 认证网关的 JWE 受保护头准入控制。所有判定都是同步纯计算，
**不执行 PBKDF2/PBES2 密钥派生、不执行解压**，调用方应在准入通过之后再做
昂贵操作，从源头挡住 `p2c=10 亿` 的 CPU 耗尽攻击与高压缩比 zip 炸弹。

## 准入规则

- **PBES2 算法**（`PBES2-HS256+A128KW`、`PBES2-HS384+A192KW`、
  `PBES2-HS512+A256KW`）：
  - `p2c` 必须是安全整数，且在 `1000` 到 `budget.maxP2c` 之间（含边界）。
  - `p2s` 必须是无填充 base64url 字符串，解码后长度在 8–64 字节（含边界）。
- **禁用算法**：`dir` 与 `RSA1_5` 直接拒绝（`ALG_FORBIDDEN`）。
- **zip**：只允许缺省（不压缩）或 `DEF`。
  - `budget.maxDecompressedBytes` 是调用方给出的解压后输出上限。
  - 按 `maxDecompressedBytes / 压缩载荷长度` 折算压缩比，**超过 100 倍拒绝，
    恰好 100 倍放行**（用除法避免大整数乘法精度问题）。
  - 解压预算为 0 时不允许出现 `DEF` 头。
- **crit**：必须是非空字符串数组、无重复、不含 RFC 注册参数名；列出的每个
  名称都必须实际出现在头中，且包含在 `budget.understoodCriticalHeaders` 里，
  否则按 RFC 7516 拒绝。
- **重复头参数名**：内置递归下降 JSON 解析器在语法层检测同一对象内的重复键
  （标准 `JSON.parse` 会静默覆盖，无法发现），compact 序列化的 base64url 头
  同样生效。
- 上述检查全部在任何密钥派生之前完成；本包不依赖 `node:crypto` / `zlib`。

## 安装与导入

零运行时依赖。Node >= 22.6 可直接运行 TS 源（`--experimental-strip-types`），
也可用 `tsc` 按 `tsconfig.json` 编译到 `dist/` 后按 `package.json` 的
`exports` 导入。

```ts
import {
  admitJweHeader,
  admitJweHeaderOrThrow,
  JweHeaderAdmissionError,
  type AdmissionBudget,
} from 'jwe-header-admission';
```

## 用法

```ts
const budget: AdmissionBudget = {
  maxP2c: 100_000,              // 网关愿意承担的 PBKDF2 迭代上限
  maxDecompressedBytes: 1_000_000, // 解压后最多 1 MiB
  understoodCriticalHeaders: ['x-tenant'], // 网关理解的 crit 扩展
};

// 入参既可以是受保护头 JSON 字符串，也可以是 JWE compact 的第一段（base64url）。
const decision = admitJweHeader(protectedHeader, ciphertextByteLength, budget);
if (!decision.admitted) {
  // decision.code / decision.message / decision.parameter 可直接映射为 4xx
  throw new JweHeaderAdmissionError(decision.code!, decision.message!);
}

// 只有到这里才允许执行 PBES2 派生与 DEF 解压。
```

偏好异常风格时用 `admitJweHeaderOrThrow`，失败抛出
`JweHeaderAdmissionError`（带 `code` 与 `parameter`）。

## 错误码

`INVALID_JSON`、`HEADER_NOT_OBJECT`、`DUPLICATE_HEADER_PARAMETER`、
`ALG_MISSING`、`ALG_FORBIDDEN`、`P2C_MISSING`、`P2C_NOT_INTEGER`、
`P2C_OUT_OF_RANGE`、`P2S_MISSING`、`P2S_NOT_BASE64URL`、
`P2S_LENGTH_OUT_OF_RANGE`、`ZIP_NOT_ALLOWED`、`DECOMPRESSED_BUDGET_ZERO`、
`COMPRESSION_RATIO_EXCEEDED`、`INVALID_COMPRESSED_LENGTH`、`BUDGET_INVALID`、
`CRIT_NOT_ARRAY`、`CRIT_EMPTY`、`CRIT_DUPLICATE`、`CRIT_NOT_STRING`、
`CRIT_REGISTERED_NAME`、`CRIT_NAME_ABSENT`、`CRIT_NAME_NOT_UNDERSTOOD`。

## 测试

```sh
npm test
# node --experimental-strip-types --test src/index.test.ts
```

覆盖题目点名的攻击向量：`p2c=10 亿`、4 字节 `p2s`、1000 倍压缩炸弹、
重复头键（含嵌套与 compact 头）、`RSA1_5`/`dir`，以及全部边界条件
（p2c=1000 与上限、p2s=8/64/65 字节、压缩比恰好 100 倍等）。
