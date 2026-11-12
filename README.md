# SKR 令牌声明策略求值

机密容器向密钥代理（KBS/SKR 服务）申请密钥时只携带一份认证令牌。本组件是密钥释放接口调用的策略求值层，针对四类攻击面：

| 攻击 | 防线 |
| --- | --- |
| 重放别的虚拟机的令牌 | 策略中对 `instance_id` 等身份声明做严格匹配 |
| 调试虚拟机令牌 | `tcb.debug` 必须严格等于布尔 `false`（字符串 `"false"` 不认） |
| 旧 TCB 令牌 | 数值版本声明 `gte` 下限，缺失或非数字一律拒绝 |
| 主机换掉运行时包裹公钥 | 包裹公钥只能取自令牌 `runtime-data`，且其哈希必须等于报告 `REPORT_DATA` |

## 设计要点

- **验签注入**：组件不持有证明机构密钥。`TokenVerifier` 由调用方注入，负责签名、过期、issuer/audience 校验；验签失败统一抛 `TokenVerificationError`。
- **严格类型求值**：声明缺失或 JSON 类型不符都算不满足。`"false" !== false`、`"12" >= 12` 不成立、`null !== undefined`。
- **嵌套策略**：`allOf` / `anyOf` 可任意嵌套，叶子是声明条件；策略在加载时经 `validatePolicy` 校验，拼写错误的操作符直接报错而不是静默放行。
- **TCB 下限**：`{ claim: "tcb.version", gte: 12 }`，另提供 `requireMinimumTcb` 便捷函数。
- **哈希绑定**：`runtime-data` 先做键名排序的规范化 JSON（`canonicalize`），再按 SHA-384（TDX，48 字节）或 SHA-256（SGX，32 字节）哈希，与 `REPORT_DATA` 做定长 `timingSafeEqual` 比较；公钥随后只从该已绑定对象中提取。
- **单点包裹**：`wrapKey` 不接受任何外部公钥参数，密钥只能被令牌证明的公钥以 RSA-OAEP 包裹。

## 目录

- `src/types.ts` — 令牌、策略、请求/响应类型
- `src/errors.ts` — 分类错误（验签/策略/绑定/包裹）
- `src/policy.ts` — 声明解析、严格比较、allOf/anyOf 求值与 trace
- `src/runtime-data.ts` — 规范化哈希、REPORT_DATA 绑定、包裹公钥提取
- `src/key-release.ts` — `KeyReleaseService` 释放流水线与 RSA-OAEP 包裹
- `test/*.test.ts` — 29 个用例，零第三方依赖（`node:test` + `node:crypto`）

## 策略语法

```ts
const policy: PolicyNode = {
  allOf: [
    { claim: "instance_id", equals: "vm-A" },
    { claim: "tcb.debug", equals: false },
    { claim: "tcb.version", gte: 12 },
    {
      anyOf: [
        { claim: "measured_boot", equals: true },
        { claim: "policy_ids", in: ["policy-1", "policy-2"] },
      ],
    },
  ],
};
```

叶子操作符：`equals` / `notEquals` / `in` / `notIn` / `exists` / `gt` / `gte` / `lt` / `lte`。声明路径用点号分段，数字段索引数组；数组声明配合 `in` 时按“任一元素命中列表”判定。

## 用法

```ts
const service = new KeyReleaseService(
  policy,
  { verifyToken: jwtVerifier, keyStore: hsmBackedStore },
  { runtimeDataHash: "SHA-384", wrappingAlgorithm: "RSA-OAEP-256" },
);

const { wrappedKey, wrappingAlgorithm } = await service.releaseKey({
  token: request.bearerToken,
  keyId: "kms-key-1",
});
```

流水线：注入验签 → 策略求值（失败抛 `PolicyNotSatisfiedError`，带完整 trace）→ runtime-data/REPORT_DATA 绑定校验 → 从 runtime-data 提取并导入 RSA 公钥 → RSA-OAEP 包裹密钥。

## 测试

```sh
npm test
```

覆盖：缺失声明、字符串/布尔与字符串/数字类型混淆、旧 TCB 下限、调试 VM、跨 VM 重放、runtime-data 换公钥、篡改 REPORT_DATA、非 RSA 包裹公钥、嵌套 anyOf 分支救援与全失败 trace。
