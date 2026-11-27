import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  type AuthorizationCertificate,
  certificateHash,
  certificatePayload,
  verifyHistoricalAuthorization,
} from '../src/ownership/cert.ts';
import { signBytes } from '../src/ownership/crypto.ts';
import { createEnvelope, transferEnvelope } from '../src/ownership/factory.ts';
import { resolveAt, resolveOwnership } from '../src/ownership/resolver.ts';
import { InMemoryKeyRegistry } from '../src/ownership/crypto.ts';
import { buildFixture, decisionMap } from './helpers.ts';

test('历史授权证书：轮换后仍可独立验证历史签名，且不依赖当前缓存', () => {
  const fx = buildFixture();
  const issuer = buildFixture();
  fx.registry.add(issuer.alice);
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );

  const unsignedCert = {
    eventHash: transfer.eventHash,
    keyId: fx.alice.keyId,
    publicKeyB64: fx.alice.publicKeyB64,
    issuedAt: 2,
    prevCertHash: null,
  };
  const cert: AuthorizationCertificate = {
    ...unsignedCert,
    issuerSignature: signBytes(issuer.alice.privateKeyPem, certificatePayload(unsignedCert)),
  };
  assert.equal(certificateHash(cert).length > 0, true);

  // 即使当前注册表移除该密钥（无缓存场景），历史证书仍携带公钥可独立验证。
  const emptyRegistry = new InMemoryKeyRegistry();
  const result = verifyHistoricalAuthorization(
    transfer,
    cert,
    issuer.alice.publicKeyB64,
    emptyRegistry,
  );
  assert.equal(result.valid, true);
  assert.equal(result.reason, 'ok');

  // 篡改证书签名后验证失败。
  const tampered: AuthorizationCertificate = { ...cert, issuerSignature: `${cert.issuerSignature}x` };
  assert.equal(
    verifyHistoricalAuthorization(transfer, tampered, issuer.alice.publicKeyB64, emptyRegistry).valid,
    false,
  );
});

test('未知密钥先等待，密钥可达后同一事件转为发布', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 2 },
  );
  const partialRegistry = new InMemoryKeyRegistry().add(fx.alice);
  const waiting = resolveOwnership([create, transfer], partialRegistry);
  assert.equal(decisionMap(waiting).get(transfer.eventHash)!.kind, 'wait');
  assert.equal(decisionMap(waiting).get(transfer.eventHash)!.reason, 'wait_unknown_key');

  partialRegistry.add(fx.bob);
  const resolved = resolveOwnership([create, transfer], partialRegistry);
  assert.equal(decisionMap(resolved).get(transfer.eventHash)!.kind, 'publish');
});

test('时点查询：asOf 之前的证据不足则等待，之后到达即可发布', () => {
  const fx = buildFixture();
  const create = createEnvelope(
    { id: fx.objectId, holder: fx.alice, recoveryPolicy: fx.policy, displayUrl: 'u' },
    { arrivedAt: 1 },
  );
  const transfer = transferEnvelope(
    { id: fx.objectId, prev: create.eventHash, gen: 1, from: fx.alice, to: fx.bob },
    { arrivedAt: 10 },
  );
  const all = [create, transfer];
  const before = resolveAt(all, fx.registry, 5);
  assert.equal(decisionMap(before).has(transfer.eventHash), false);
  const after = resolveAt(all, fx.registry, 10);
  assert.equal(decisionMap(after).get(transfer.eventHash)!.kind, 'publish');
});
