import { test } from 'node:test';
import assert from 'node:assert/strict';

import { RedemptionError } from '../src/errors.ts';
import { StaticTokenVerifier, decode } from '../src/crypto.ts';
import {
  InMemoryActiveIssuerStore,
  InMemoryIssuerDirectory,
  InMemoryRedemptionRecordStore,
  InMemoryTokenRedemptionStore,
} from '../src/storage.ts';
import {
  RedemptionService,
  type RedemptionServiceOptions,
} from '../src/redemption.ts';
import type {
  IssuerConfig,
  KeyCommitmentEntry,
  PrivateStateToken,
  RedemptionRequest,
} from '../src/types.ts';

const NOW = 1_000_000;
const TTL_MS = 1_000;
const ISSUER_A = 'issuer-a.example';
const ISSUER_B = 'issuer-b.example';
const KEY_A1 = 'key-a1';
const KEY_A2_EXPIRED = 'key-a2-expired';
const KEY_B1 = 'key-b1';
const SITE_1 = 'https://site1.example';
const SITE_2 = 'https://site2.example';

let tokenSerial = 0;

function keyEntry(
  keyId: string,
  expiryMs: number,
  notBeforeMs?: number,
): KeyCommitmentEntry {
  return {
    keyId,
    publicKey: new TextEncoder().encode(`pk-${keyId}`),
    expiryMs,
    notBeforeMs,
  };
}

function issuerConfig(
  issuerId: string,
  keyIds: string[],
  batchSize = 3,
  recordTtlMs = TTL_MS,
): IssuerConfig {
  return {
    issuerId,
    batchSize,
    recordTtlMs,
    keys: new Map(keyIds.map((id) => [id, keyEntry(id, NOW + 10_000)])),
  };
}

function makeToken(
  issuerId: string,
  keyId: string,
  tokenIndex: number,
): PrivateStateToken {
  tokenSerial += 1;
  return {
    issuerId,
    keyId,
    tokenIndex,
    signature: decode(tokenSerial.toString(16).padStart(8, '0')),
  };
}

interface Harness {
  service: RedemptionService;
  verifier: StaticTokenVerifier;
  clock: { now: number };
  tokenStore: InMemoryTokenRedemptionStore;
  recordStore: InMemoryRedemptionRecordStore;
  activeIssuerStore: InMemoryActiveIssuerStore;
}

function buildHarness(
  configs: IssuerConfig[],
  options: Partial<RedemptionServiceOptions> = {},
): Harness {
  const verifier = new StaticTokenVerifier();
  const clock = { now: NOW };
  const tokenStore = new InMemoryTokenRedemptionStore();
  const recordStore = new InMemoryRedemptionRecordStore();
  const activeIssuerStore = new InMemoryActiveIssuerStore();
  const service = new RedemptionService({
    verifier,
    issuerDirectory: new InMemoryIssuerDirectory(configs),
    tokenStore,
    recordStore,
    activeIssuerStore,
    clock: () => clock.now,
    ...options,
  });
  return { service, verifier, clock, tokenStore, recordStore, activeIssuerStore };
}

function acceptTokens(
  verifier: StaticTokenVerifier,
  tokens: PrivateStateToken[],
): void {
  for (const token of tokens) {
    verifier.accept(token);
  }
}

function redemptionRequest(
  topLevelSite: string,
  tokens: PrivateStateToken[],
): RedemptionRequest {
  return { topLevelSite, tokens };
}

function configsWithExpiredKey(): IssuerConfig[] {
  const config = issuerConfig(ISSUER_A, [KEY_A1]);
  config.keys.set(KEY_A2_EXPIRED, keyEntry(KEY_A2_EXPIRED, NOW - 1));
  return [config, issuerConfig(ISSUER_B, [KEY_B1])];
}

async function assertRejected(
  promise: Promise<unknown>,
  code: string,
): Promise<RedemptionError> {
  let thrown: unknown;
  try {
    await promise;
  } catch (err) {
    thrown = err;
  }
  assert.ok(thrown instanceof RedemptionError, '预期抛出 RedemptionError');
  const error = thrown;
  assert.equal(error.code, code);
  return error;
}

test('正常批次可兑换，生成 TTL 正确的兑换记录并记账站点发行方', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const tokens = [
    makeToken(ISSUER_A, KEY_A1, 1),
    makeToken(ISSUER_A, KEY_A1, 2),
  ];
  acceptTokens(harness.verifier, tokens);

  const result = await harness.service.redeem(redemptionRequest(SITE_1, tokens));

  assert.equal(result.redeemedKeys.length, 2);
  assert.equal(result.record.issuerId, ISSUER_A);
  assert.equal(result.record.redeemedAtMs, NOW);
  assert.equal(result.record.expiresAtMs, NOW + TTL_MS);
  assert.equal(harness.tokenStore.activeSize(NOW), 2);
  assert.equal(
    await harness.activeIssuerStore.isActive(SITE_1, ISSUER_A, NOW),
    true,
  );
});

test('重复兑换：同一（发行方、密钥 ID、令牌序号）在 TTL 内只能成功一次', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 7);
  acceptTokens(harness.verifier, [token]);

  const first = await harness.service.redeem(redemptionRequest(SITE_1, [token]));
  assert.equal(first.record.issuerId, ISSUER_A);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    'DUPLICATE_TOKEN',
  );

  const sameKeyDifferentSite = makeToken(ISSUER_A, KEY_A1, 7);
  acceptTokens(harness.verifier, [sameKeyDifferentSite]);
  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_2, [sameKeyDifferentSite])),
    'DUPLICATE_TOKEN',
  );
});

test('重复兑换：同一批次内出现重复令牌直接拒绝且不产生记账', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 3);
  acceptTokens(harness.verifier, [token]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [token, token])),
    'DUPLICATE_TOKEN',
  );
  assert.equal(harness.tokenStore.activeSize(NOW), 0);
});

test('重复兑换：批次中途发现已兑换令牌时回滚已占用键', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const first = makeToken(ISSUER_A, KEY_A1, 10);
  const second = makeToken(ISSUER_A, KEY_A1, 11);
  acceptTokens(harness.verifier, [first, second]);

  await harness.service.redeem(redemptionRequest(SITE_1, [first]));

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [second, first])),
    'DUPLICATE_TOKEN',
  );
  assert.equal(harness.tokenStore.activeSize(NOW), 1);

  const replaySecond = makeToken(ISSUER_A, KEY_A1, 11);
  acceptTokens(harness.verifier, [replaySecond]);
  const result = await harness.service.redeem(
    redemptionRequest(SITE_1, [replaySecond]),
  );
  assert.equal(result.redeemedKeys.length, 1);
});

test('跨发行方：混合发行方批次被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const tokenA = makeToken(ISSUER_A, KEY_A1, 1);
  const tokenB = makeToken(ISSUER_B, KEY_B1, 1);
  acceptTokens(harness.verifier, [tokenA, tokenB]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [tokenA, tokenB])),
    'HETEROGENEOUS_ISSUER',
  );
  assert.equal(harness.tokenStore.activeSize(NOW), 0);
});

test('跨发行方：固定发行方服务拒绝其它发行方令牌', async () => {
  const harness = buildHarness(
    [issuerConfig(ISSUER_A, [KEY_A1]), issuerConfig(ISSUER_B, [KEY_B1])],
    { fixedIssuerId: ISSUER_A },
  );
  const tokenB = makeToken(ISSUER_B, KEY_B1, 5);
  acceptTokens(harness.verifier, [tokenB]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [tokenB])),
    'HETEROGENEOUS_ISSUER',
  );
});

test('跨发行方：来自未登记发行方的令牌被拒绝', async () => {
  const harness = buildHarness([issuerConfig(ISSUER_A, [KEY_A1])]);
  const rogue = makeToken('issuer-rogue.example', 'k1', 1);
  acceptTokens(harness.verifier, [rogue]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [rogue])),
    'UNKNOWN_ISSUER',
  );
});

test('过期密钥：使用已过期 keyId 的令牌被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A2_EXPIRED, 1);
  acceptTokens(harness.verifier, [token]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    'KEY_EXPIRED',
  );
  assert.equal(harness.tokenStore.activeSize(NOW), 0);
});

test('密钥不在当前承诺内：未知 keyId 被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, 'key-rotated-away', 1);
  acceptTokens(harness.verifier, [token]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    'UNKNOWN_KEY',
  );
});

test('未生效密钥：notBefore 之前的 keyId 被拒绝', async () => {
  const config = issuerConfig(ISSUER_A, [KEY_A1]);
  config.keys.set('key-future', keyEntry('key-future', NOW + 10_000, NOW + 5_000));
  const harness = buildHarness([config]);
  const token = makeToken(ISSUER_A, 'key-future', 1);
  acceptTokens(harness.verifier, [token]);

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    'KEY_NOT_YET_VALID',
  );
});

test('超额批量：令牌数超过承诺 batchSize 被拒绝', async () => {
  const config = issuerConfig(ISSUER_A, [KEY_A1], 2);
  const harness = buildHarness([config]);
  const tokens = [
    makeToken(ISSUER_A, KEY_A1, 1),
    makeToken(ISSUER_A, KEY_A1, 2),
    makeToken(ISSUER_A, KEY_A1, 3),
  ];
  acceptTokens(harness.verifier, tokens);

  const error = await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, tokens)),
    'BATCH_TOO_LARGE',
  );
  assert.deepEqual(error.details, { batchSize: 2, actual: 3 });
  assert.equal(harness.tokenStore.activeSize(NOW), 0);
});

test('空批次被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [])),
    'EMPTY_BATCH',
  );
});

test('密码验证注入：无法通过验证器的令牌被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const unregistered = makeToken(ISSUER_A, KEY_A1, 1);
  // 故意不登记到 verifier，模拟无法通过密码验证的令牌。
  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [unregistered])),
    'INVALID_SIGNATURE',
  );

  const blocked = makeToken(ISSUER_A, KEY_A1, 2);
  acceptTokens(harness.verifier, [blocked]);
  harness.verifier.rejectSignature(blocked.signature);
  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [blocked])),
    'INVALID_SIGNATURE',
  );
  assert.equal(harness.tokenStore.activeSize(NOW), 0);
});

test('兑换记录：有效期内只能出示一次，重复出示被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 20);
  acceptTokens(harness.verifier, [token]);
  const { record } = await harness.service.redeem(
    redemptionRequest(SITE_1, [token]),
  );

  const presented = await harness.service.presentRecord(record.recordId);
  assert.equal(presented.recordId, record.recordId);
  await assertRejected(
    harness.service.presentRecord(record.recordId),
    'RECORD_ALREADY_PRESENTED',
  );
  assert.equal(harness.recordStore.isPresented(record.recordId), true);
});

test('兑换记录：超过发行方 TTL 后出示被拒绝，未知记录被拒绝', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 21);
  acceptTokens(harness.verifier, [token]);
  const { record } = await harness.service.redeem(
    redemptionRequest(SITE_1, [token]),
  );

  harness.clock.now = record.expiresAtMs;
  await assertRejected(
    harness.service.presentRecord(record.recordId),
    'RECORD_EXPIRED',
  );
  await assertRejected(
    harness.service.presentRecord('missing-record'),
    'RECORD_NOT_FOUND',
  );
});

test('去重 TTL：超过发行方配置有效期后同一令牌可再次兑换', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 30);
  acceptTokens(harness.verifier, [token]);

  const first = await harness.service.redeem(redemptionRequest(SITE_1, [token]));
  harness.clock.now = first.record.expiresAtMs;

  const second = await harness.service.redeem(redemptionRequest(SITE_1, [token]));
  assert.equal(second.record.expiresAtMs, harness.clock.now + TTL_MS);
});

test('站点上限：同一顶层站点同时有效的发行方不超过 2 个', async () => {
  const issuerC = 'issuer-c.example';
  const harness = buildHarness([
    issuerConfig(ISSUER_A, [KEY_A1]),
    issuerConfig(ISSUER_B, [KEY_B1]),
    issuerConfig(issuerC, ['key-c1']),
  ]);

  const tokenA = makeToken(ISSUER_A, KEY_A1, 1);
  const tokenB = makeToken(ISSUER_B, KEY_B1, 1);
  const tokenC = makeToken(issuerC, 'key-c1', 1);
  acceptTokens(harness.verifier, [tokenA, tokenB, tokenC]);

  await harness.service.redeem(redemptionRequest(SITE_1, [tokenA]));
  await harness.service.redeem(redemptionRequest(SITE_1, [tokenB]));

  await assertRejected(
    harness.service.redeem(redemptionRequest(SITE_1, [tokenC])),
    'TOO_MANY_ACTIVE_ISSUERS',
  );

  // 不同站点互不影响。
  const onOtherSite = await harness.service.redeem(
    redemptionRequest(SITE_2, [tokenC]),
  );
  assert.equal(onOtherSite.record.topLevelSite, SITE_2);
});

test('站点上限：发行方过期腾出名额后可接受新发行方', async () => {
  const issuerC = 'issuer-c.example';
  const harness = buildHarness([
    issuerConfig(ISSUER_A, [KEY_A1]),
    issuerConfig(ISSUER_B, [KEY_B1]),
    issuerConfig(issuerC, ['key-c1']),
  ]);

  const tokenA = makeToken(ISSUER_A, KEY_A1, 1);
  const tokenB = makeToken(ISSUER_B, KEY_B1, 1);
  acceptTokens(harness.verifier, [tokenA, tokenB]);
  await harness.service.redeem(redemptionRequest(SITE_1, [tokenA]));
  const recordB = await harness.service.redeem(
    redemptionRequest(SITE_1, [tokenB]),
  );

  harness.clock.now = recordB.record.expiresAtMs;

  const tokenC = makeToken(issuerC, 'key-c1', 1);
  acceptTokens(harness.verifier, [tokenC]);
  const result = await harness.service.redeem(
    redemptionRequest(SITE_1, [tokenC]),
  );
  assert.equal(result.record.issuerId, issuerC);
});

test('并发重放：同一令牌的并发兑换至多一次成功', async () => {
  const harness = buildHarness(configsWithExpiredKey());
  const token = makeToken(ISSUER_A, KEY_A1, 40);
  acceptTokens(harness.verifier, [token]);

  const outcomes = await Promise.allSettled([
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
    harness.service.redeem(redemptionRequest(SITE_1, [token])),
  ]);

  const fulfilled = outcomes.filter((o) => o.status === 'fulfilled');
  const rejected = outcomes.filter((o) => o.status === 'rejected');
  assert.equal(fulfilled.length, 1);
  assert.equal(rejected.length, 2);
  for (const outcome of rejected) {
    assert.equal((outcome.reason as RedemptionError).code, 'DUPLICATE_TOKEN');
  }
});

test('ID 工厂可注入：兑换记录使用自定义 recordId', async () => {
  const harness = buildHarness(configsWithExpiredKey(), {
    idFactory: () => 'fixed-record-id',
  });
  const token = makeToken(ISSUER_A, KEY_A1, 50);
  acceptTokens(harness.verifier, [token]);

  const result = await harness.service.redeem(redemptionRequest(SITE_1, [token]));
  assert.equal(result.record.recordId, 'fixed-record-id');
});
