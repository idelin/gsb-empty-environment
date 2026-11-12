# 私有状态令牌兑换记账

攻击者想重复兑换同一枚 Private State Token，或拿另一发行方的令牌来兑换。为TypeScript发行方服务写可导入的兑换组件：令牌按（发行方、密钥ID、令牌序号）只能兑换一次；密钥ID 须在该发行方当前密钥承诺内且未过期；单次发放批量不超过承诺的 batchsize；兑换记录有效期取发行方配置，兑换记录不能被再次兑换；每个顶层站点同时有效的发行方不超过2个。密码验证注入，测重复兑换、跨发行方、过期密钥、超额批量。

## 组件结构

可从包入口 `index.ts` 导入，全部外部依赖均通过构造函数注入：

- `src/types.ts`：领域类型与存储/目录接口。
- `src/crypto.ts`：`TokenVerifier` 密码验证接口（生产对接真实验签，测试用 `StaticTokenVerifier`）。
- `src/storage.ts`：内存版发行方目录、令牌去重、兑换记录、站点活跃发行方存储。
- `src/redemption.ts`：`RedemptionService`，承载全部兑换策略。
- `src/errors.ts`：`RedemptionError` 及稳定错误码。

## 强制的兑换规则

1. 令牌唯一键为（`issuerId`、`keyId`、`tokenIndex`），在发行方配置的 `recordTtlMs` 内只能成功兑换一次（`TokenRedemptionStore.claim` 原子占用，重复返回 `DUPLICATE_TOKEN`）。
2. `keyId` 必须存在于该发行方当前密钥承诺，且满足 `notBefore`/`expiryMs` 时间窗，否则返回 `UNKNOWN_KEY` / `KEY_NOT_YET_VALID` / `KEY_EXPIRED`。
3. 单批令牌数超过承诺 `batchSize` 返回 `BATCH_TOO_LARGE`；空批次返回 `EMPTY_BATCH`。
4. 同一批次只能包含同一发行方的令牌（`HETEROGENEOUS_ISSUER`），未登记发行方返回 `UNKNOWN_ISSUER`；可配置 `fixedIssuerId` 锁定单发行方服务。
5. 兑换记录 TTL 取发行方配置；记录在有效期内只能出示一次（`presentRecord`，重复返回 `RECORD_ALREADY_PRESENTED`，过期返回 `RECORD_EXPIRED`）。
6. 每个顶层站点同时有效的发行方不超过 2 个，超限返回 `TOO_MANY_ACTIVE_ISSUERS`；过期发行方自动腾出名额。

## 使用示例

```ts
import {
  RedemptionService,
  StaticTokenVerifier,
  InMemoryIssuerDirectory,
} from './index.ts';

const verifier = new StaticTokenVerifier(); // 生产环境替换为真实 TokenVerifier
const service = new RedemptionService({
  verifier,
  issuerDirectory: new InMemoryIssuerDirectory([issuerConfig]),
  // tokenStore / recordStore / activeIssuerStore / clock / idFactory 均可注入
});

const { record } = await service.redeem({ topLevelSite, tokens });
await service.presentRecord(record.recordId); // 同一记录再次出示会被拒绝
```

生产部署时实现 `TokenRedemptionStore`、`RedemptionRecordStore`、`ActiveIssuerStore`
三个接口并接入带 TTL 的数据库（如 Redis `SET NX PX`），即可保持同样的原子语义。

## 测试

```sh
npm test
```

`test/redemption.test.ts` 覆盖重复兑换（含批内重复、回滚、并发重放）、跨发行方、
过期/未生效/未知密钥、超额批量、密码验证失败、兑换记录重放与 TTL、站点发行方上限等 20 个场景。
