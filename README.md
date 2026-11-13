# EIP-712 类型化数据签名校验（中继服务）

链下签名授权的合约调用若未在签名里绑定链标识、目标合约和每个签名者的
计数，就可能被搬到另一条链、另一个合约，或被重复提交。本包为 TypeScript
中继服务提供可导入的 EIP-712 类型化数据签名校验能力，**keccak256 与
ECDSA 地址恢复均由调用方注入**，核心包本身不绑定任何加密库。

## 设计要点

- 域分隔符为 `hashStruct(EIP712Domain)`，域字段只包含实际声明出现的
  `name`、`version`、`chainId`、`verifyingContract`，类型字符串按
  `name → version → chainId → verifyingContract` 的声明顺序拼接。
- 消息哈希为
  `keccak256(0x19 0x01 ‖ 域分隔符 ‖ hashStruct(message))`。
- `bytes` 与 `string` 取其 keccak 哈希；数组为各元素 32 字节编码拼接后
  再哈希；嵌套结构取其自身的 `hashStruct`。
- 类型字符串主类型成员按声明顺序排列，引用到的其它结构按结构名字典序排列。
- 中继强制域绑定：`chainId` 必须等于配置链、`verifyingContract` 必须等于
  目标合约，任一缺失即拒绝。
- 消息 `nonce` 必须等于链上当前值，提交成功后原子递增；`deadline` 已过
  （小于当前时间）即拒绝，恰好等于当前时间视为有效。
- 恢复出的地址必须等于消息里声明的签名者。
- 签名 `s` 落在曲线阶高半区（malleable）或 `v` 不是 27/28 时拒绝，
  且这些检查在调用地址恢复之前完成。

## 安装与导入

构建产物在 `dist/`（`tsc` 输出），源码在 `src/`。

```ts
import {
  TypedDataRelayer,
  InMemoryNonceStore,
  SignatureVerificationError,
  VerificationErrorCode,
  hashTypedData,
} from "typed-data-relayer";
```

## 注入依赖

调用方提供两个函数，便于替换为经过审计的库（如 viem、ethers、
`@noble/curves` 等）：

```ts
import { keccak256 } from "some-keccak-lib";
import { recoverAddress } from "some-secp256k1-lib";

const relayer = new TypedDataRelayer({
  chainId: 1n,
  targetContract: "0x1111111111111111111111111111111111111111",
  keccak256,                 // (input: Uint8Array) => Uint8Array (32 字节)
  recoverAddress,            // (digest, 65 字节 r||s||v(v 为 0/1)) => 地址 hex
  nonceStore: new InMemoryNonceStore(), // 生产环境实现为链上读写
  // 可选：自定义消息字段名与时钟
  // nonceField: "nonce", deadlineField: "deadline", signerField: "signer",
  // now: () => BigInt(Math.floor(Date.now() / 1000)),
});
```

`NonceStore.checkAndIncrement` 必须是原子的“比对并递增”：仅当链上当前值
等于期望 nonce 时才递增并返回 `true`，否则返回 `false`，避免 TOCTOU 竞争。

## 处理一次中继请求

```ts
try {
  const result = await relayer.relay(
    {
      domain: {
        name: "PairwiseGSB",
        version: "1",
        chainId: 1n,
        verifyingContract: "0x1111111111111111111111111111111111111111",
      },
      types: AUTHORIZATION_TYPES,
      primaryType: "Authorization",
      message: { signer, target, value, nonce, deadline, data },
      signature, // 0x 前缀 65 字节或 Uint8Array，v 为 27/28
    },
    // nonce 已原子递增后触发，可在此广播链上交易
    async (_request, result) => broadcast(result.signer, result.nonce),
  );
  console.log("accepted", result.signer, result.nonce);
} catch (error) {
  if (error instanceof SignatureVerificationError) {
    // error.code 取自 VerificationErrorCode，映射为固定 API 响应
  }
}
```

校验顺序：域绑定 → 签名者/nonce/deadline 字段存在性 → 签名格式与
malleability 检查 → EIP-712 摘要与地址恢复比对 → 原子 nonce 比对递增 →
成功回调。任何一步失败都不会消费 nonce。

## 纯哈希与纯签名校验

不需要中继状态机时可直接使用底层函数：

```ts
const digest = hashTypedData(keccak256, domain, types, "Authorization", message);

await verifyTypedData(
  { keccak256, recoverAddress },
  { domain, types, primaryType: "Authorization", message, expectedSigner, signature },
);
```

## 错误码

`VerificationErrorCode` 覆盖：`CHAIN_ID_MISMATCH`、
`MISSING_CHAIN_ID`、`VERIFYING_CONTRACT_MISMATCH`、
`MISSING_VERIFYING_CONTRACT`、`NONCE_MISMATCH`、`MISSING_NONCE`、
`DEADLINE_EXPIRED`、`MISSING_DEADLINE`、`SIGNER_MISMATCH`、
`HIGH_S`、`INVALID_V`、`INVALID_SIGNATURE_LENGTH`、
`INVALID_SIGNATURE_ENCODING` 等。

## 测试

零外部依赖。`test/support/` 提供纯 TypeScript 的 Keccak-256 与
secp256k1（RFC6979 确定性签名、公钥恢复、以太坊地址派生）作为注入参考
实现，仅用于测试，生产环境请勿直接使用。

```bash
npm test
```

测试覆盖五类攻击与边界：

- 换链重放（`chainId` 不同 / 缺失 → 拒绝，不消费 nonce）
- 换合约重放（`verifyingContract` 不同 / 缺失 → 拒绝）
- nonce 复用与跳号（第二次提交 / 不等于链上当前值 → 拒绝）
- 过期 deadline（小于当前时间 → 拒绝；相等 → 允许）
- 高 `s` 值与非法 `v`（恢复前即拒绝），以及签名者不匹配、消息篡改、
  签名长度/编码错误、独立签名者 nonce 隔离等。

## 构建

```bash
npm run build   # tsc 输出到 dist/
```
