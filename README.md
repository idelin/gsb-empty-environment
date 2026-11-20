# 查询响应证明校验包

供 TypeScript 网关在验收索引者查询响应时调用。包内负责请求/响应哈希、部署绑定、区块范围、`errors` 检查和分配归属判断；EIP-712 签名恢复与链上活跃分配读取由调用方注入。

## 快速使用

```ts
import {
  verifyResponseProof,
  type VerifierDependencies,
} from "./src/index.ts";

const deps: VerifierDependencies = {
  // 调用方用任意 EIP-712/ECDSA 库恢复地址。digest 已是完整 EIP-712 digest。
  recoverSigner: async ({ typedData, digest, signature }) => {
    return recoverTypedDataAddress({
      domain: typedData.domain,
      types: typedData.types,
      primaryType: typedData.primaryType,
      message: typedData.message,
      signature,
    });
  },

  // 返回该 indexer + deployment 在当前链头的活跃分配；也可返回分配记录数组。
  allocationTable: {
    async getActiveAllocations({ indexer, deployment, chainHead }) {
      return chainReader.activeAllocations(indexer, deployment, chainHead);
    },
  },
};

const result = await verifyResponseProof(
  {
    requestBody: rawRequestBody,   // 实际发出的请求体
    responseBody: rawResponseBody, // 实际收到的响应体
    proof: {
      requestHash,
      responseHash,
      deployment,
      signature,
    },
    query: {
      indexer,
      deployment,
      numberGte,
    },
    chainHead: currentChainHead,
  },
  deps,
);

if (!result.ok) {
  // result.code 是稳定的失败码，可直接用于追责/告警。
  throw new Error(`${result.code}: ${result.message}`);
}
```

`verifyResponseProof` 永远返回结果对象而不抛业务异常；需要抛异常时使用 `assertResponseProof`。

## 验收条件

全部条件必须同时成立：

- `proof.requestHash === keccak256(实际请求体)`。
- `proof.responseHash === keccak256(实际响应体)`。
- EIP-712 恢复出的 signer 是 `indexer + deployment` 在当前链头的活跃分配 ID。
- `proof.deployment === query.deployment`。
- 响应 `_meta.block.number >= query.numberGte`，且 `<= chainHead`。
- 响应不含 `errors` 成员（`null` / `undefined` 视为无 errors）。

`_meta.block.number` 支持写在 `data._meta.block.number` 或顶层 `_meta.block.number`；两处同时出现但不一致时拒绝。

## EIP-712 格式

默认 domain 为：

```ts
{ name: "GraphGatewayResponseProof", version: "1" }
```

网关应通过 `eip712Domain` 注入自己固定的 domain（建议至少固定 `chainId` 和 `verifyingContract`）。Proof 的 primary type 为：

```text
ResponseProof(bytes32 requestHash,bytes32 responseHash,bytes32 deployment)
```

包导出 `buildResponseProofTypedData`、`responseProofDigest` 和 `hashTypedDataHex`，签名方与校验方必须使用同一 domain 和同一类型定义。

## 请求体与响应体哈希

- `string`：按 UTF-8 原始字节哈希，不重新序列化。
- `Uint8Array` / `ArrayBuffer`：按原始字节哈希。
- JavaScript 对象：按键名排序后的规范 JSON 哈希，便于测试和内存调用；网关生产路径应优先传原始 wire bytes。

## 分配表注入

`allocationTable` 可以是函数，也可以实现 `getActiveAllocations`、`getAllocations` 或 `getActiveAllocation`。`getAllocations` 可返回全量记录，由校验包按 `closed` / `closedAtBlock` 过滤：

```ts
type AllocationResult =
  | string
  | {
      allocationId?: string;
      id?: string;
      indexer?: string;
      deployment?: string;
      active?: boolean;
      closed?: boolean;
      closedAtBlock?: number | bigint | string | null;
    }
  | Array<string | AllocationRecord>
  | null;
```

数组记录中，`active: false`、`closed: true` 或 `closedAtBlock <= chainHead` 的记录不会被视为活跃分配。测试可使用内置的 `InMemoryAllocationTable`。

## 失败码

`REQUEST_HASH_MISMATCH`、`RESPONSE_HASH_MISMATCH`、`DEPLOYMENT_MISMATCH`、`SIGNER_NOT_ACTIVE_ALLOCATION`、`RESPONSE_HAS_ERRORS`、`BLOCK_BELOW_MINIMUM`、`BLOCK_ABOVE_CHAIN_HEAD`、`BLOCK_NUMBER_MISSING`、`BLOCK_NUMBER_INVALID`、`SIGNATURE_RECOVERY_FAILED`、`ALLOCATION_LOOKUP_FAILED` 等。

## 测试

测试覆盖换响应体、已关闭分配、他部署证明、未来区块、含 `errors` 响应、他查询证明和低于 `number_gte` 的区块。

```bash
npm test
```
