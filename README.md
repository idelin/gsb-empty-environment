# 交易数据哈希

出示时附带的交易数据与钱包展示给用户的字节不一致。kb-jwt或设备签名覆盖交易数据的哈希；哈希不符或缺失则拒绝出示。哈希覆盖当前交易字节时允许出示。由钱包调用可导入的TypeScript库。展示一份交易数据，签名另一份的哈希。

## 用法

```ts
import { hashTransactionData, verifyPresentation } from "./index.ts";

const result = verifyPresentation({
  presentedTransactionData: txBytes, // 当前出示的交易数据字节
  kbJwt,                             // 和/或 deviceSignature + devicePublicKey
});
if (!result.allowed) throw new Error(result.reason);
```

- `hashTransactionData(data, alg?)`：计算交易字节哈希（默认 sha-256）。
- `verifyPresentation(args)`：kb-jwt 的 `transaction_data_hashes` 或设备签名（Ed25519，覆盖哈希）任一覆盖当前交易字节即允许；哈希不符或缺失则拒绝。
- `demo.ts`：演示“展示一份交易数据、签名另一份的哈希”被拒绝，及一致时被允许。

运行：`npm test`；演示：`node --experimental-strip-types demo.ts`。
