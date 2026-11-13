import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { resolveKeySource } from '../index.ts';
import type {
  JwsHeaderLike,
  KeyRegistry,
  RegisteredKeyEntry,
} from '../index.ts';

async function exportPublicJwk(): Promise<JsonWebKey> {
  const keyPair = await crypto.subtle.generateKey(
    { name: 'ECDSA', namedCurve: 'P-256' },
    true,
    ['sign', 'verify'],
  );
  return crypto.subtle.exportKey('jwk', keyPair.publicKey);
}

async function buildRegistry(): Promise<{ registry: KeyRegistry; entry: RegisteredKeyEntry; jwk: JsonWebKey }> {
  const jwk = await exportPublicJwk();
  const entry: RegisteredKeyEntry = {
    kid: 'tenant-a',
    publicJwk: jwk,
    allowedAlgs: ['ES256'],
    allowedHost: 'keys.example.com',
    allowedPathPrefix: '/v1/keys',
  };
  return { entry, jwk, registry: { [entry.kid]: entry } };
}

describe('resolveKeySource - 自带 jwk / x5c', () => {
  it('自带 jwk 与登记公钥相同时，仍只返回登记表里的密钥', async () => {
    const { registry, jwk } = await buildRegistry();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jwk: structuredClone(jwk) },
      registry,
    );
    assert.equal(decision.kind, 'use-registered-key');
    if (decision.kind === 'use-registered-key') {
      assert.equal(decision.publicJwk, registry['tenant-a']!.publicJwk);
    }
  });

  it('自带 jwk 与登记公钥不同时按伪造令牌拒绝 (forged_jwk)', async () => {
    const { registry } = await buildRegistry();
    const attackerJwk = await exportPublicJwk();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jwk: attackerJwk },
      registry,
    );
    assert.deepEqual(decision, {
      kind: 'reject',
      reason: 'forged_jwk',
      detail: decision.kind === 'reject' ? decision.detail : '',
    });
  });

  it('篡改登记 jwk 的公钥参数同样判定为伪造', async () => {
    const { registry, jwk } = await buildRegistry();
    const tampered = structuredClone(jwk);
    tampered.y = 'A'.repeat((tampered.y ?? '').length);
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jwk: tampered },
      registry,
    );
    assert.equal(decision.kind, 'reject');
    assert.equal((decision as { reason: string }).reason, 'forged_jwk');
  });

  it('头里的 jwk 不是对象或缺少公钥成员时拒绝，而不是尝试使用它', async () => {
    const { registry } = await buildRegistry();
    for (const badJwk of ['not-an-object', 42, null, { kty: 'EC' }]) {
      const decision = await resolveKeySource(
        { kid: 'tenant-a', alg: 'ES256', jwk: badJwk } as JwsHeaderLike,
        registry,
      );
      assert.equal(decision.kind, 'reject');
      assert.equal((decision as { reason: string }).reason, 'invalid_header_jwk');
    }
  });

  it('x5c 永远被忽略，不影响使用登记密钥', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      {
        kid: 'tenant-a',
        alg: 'ES256',
        x5c: ['MIIBIjANBgkqhkiG...attacker-controlled-cert'],
      },
      registry,
    );
    assert.equal(decision.kind, 'use-registered-key');
  });
});

describe('resolveKeySource - kid 与 alg 门禁', () => {
  it('kid 缺失时拒绝', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource({ alg: 'ES256' }, registry);
    assert.equal((decision as { reason: string }).reason, 'missing_kid');
  });

  it('kid 不在登记表内时拒绝且不会产生抓取决定', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      {
        kid: 'stranger',
        alg: 'ES256',
        jku: 'https://keys.example.com/v1/keys/stranger.jwk',
      },
      registry,
    );
    assert.equal(decision.kind, 'reject');
    assert.equal((decision as { reason: string }).reason, 'unknown_kid');
  });

  it('alg 缺失或不在登记算法集合内时拒绝', async () => {
    const { registry } = await buildRegistry();
    assert.equal(
      (await resolveKeySource({ kid: 'tenant-a' }, registry) as { reason: string }).reason,
      'missing_alg',
    );
    const notAllowed = await resolveKeySource(
      { kid: 'tenant-a', alg: 'RS256' },
      registry,
    );
    assert.equal((notAllowed as { reason: string }).reason, 'alg_not_allowed');
  });
});

describe('resolveKeySource - jku / x5u 来源策略', () => {
  const validJku = 'https://keys.example.com/v1/keys/tenant-a.jwk';

  it('来源精确匹配时返回 fetch-url 决定（本身不发起请求）', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: validJku },
      registry,
    );
    assert.equal(decision.kind, 'fetch-url');
    if (decision.kind === 'fetch-url') {
      assert.equal(decision.header, 'jku');
      assert.equal(decision.url, validJku);
    }
  });

  it('主机不分大小写，路径逐字比较', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://KEYS.Example.COM/v1/keys/tenant-a.jwk' },
      registry,
    );
    assert.equal(decision.kind, 'fetch-url');
  });

  it('jku 指向别的路径（前缀不在段边界上）时拒绝', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://keys.example.com/v1/keys-evil/tenant-a.jwk' },
      registry,
    );
    assert.equal(decision.kind, 'reject');
    assert.equal((decision as { reason: string }).reason, 'invalid_jku');
    assert.match((decision as { detail: string }).detail, /path_prefix_mismatch/);
  });

  it('userinfo 绕过尝试被拒绝', async () => {
    const { registry } = await buildRegistry();
    const decision = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://attacker@keys.example.com/v1/keys/tenant-a.jwk' },
      registry,
    );
    assert.equal((decision as { reason: string }).reason, 'invalid_jku');
    assert.match((decision as { detail: string }).detail, /has_userinfo/);
  });

  it('查询串、片段、.. 段、非默认端口、非 https 一律拒绝', async () => {
    const { registry } = await buildRegistry();
    const cases: Array<[string, RegExp]> = [
      ['https://keys.example.com/v1/keys/tenant-a.jwk?download=true', /has_query/],
      ['https://keys.example.com/v1/keys/tenant-a.jwk#section', /has_fragment/],
      ['https://keys.example.com/v1/keys/../keys/tenant-a.jwk', /has_dotdot_segment/],
      ['https://keys.example.com/v1/keys/%2e%2e/tenant-a.jwk', /has_dotdot_segment/],
      ['https://keys.example.com:8443/v1/keys/tenant-a.jwk', /non_default_port/],
      ['http://keys.example.com/v1/keys/tenant-a.jwk', /not_https/],
      ['https://evil.example.org/v1/keys/tenant-a.jwk', /host_mismatch/],
    ];
    for (const [jku, expected] of cases) {
      const decision = await resolveKeySource(
        { kid: 'tenant-a', alg: 'ES256', jku },
        registry,
      );
      assert.equal(decision.kind, 'reject', jku);
      assert.equal((decision as { reason: string }).reason, 'invalid_jku', jku);
      assert.match((decision as { detail: string }).detail, expected, jku);
    }
  });

  it('路径前缀必须落在段边界上，且反斜杠不能绕过主机/路径检查', async () => {
    const { registry } = await buildRegistry();
    const sibling = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://keys.example.com/v1/keysbackdoor/x.jwk' },
      registry,
    );
    assert.equal((sibling as { reason: string }).reason, 'invalid_jku');

    const inside = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://keys.example.com/v1/keys/deep/x.jwk' },
      registry,
    );
    assert.equal(inside.kind, 'fetch-url');

    const backslash = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: 'https://keys.example.com\\@evil.example.org/v1/keys/x.jwk' },
      registry,
    );
    assert.equal(backslash.kind, 'reject');
  });

  it('x5u 走同一套来源策略，且与 jku 同时出现时拒绝', async () => {
    const { registry } = await buildRegistry();
    const allowed = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', x5u: validJku.replace('.jwk', '.pem') },
      registry,
    );
    assert.equal(allowed.kind, 'fetch-url');
    if (allowed.kind === 'fetch-url') {
      assert.equal(allowed.header, 'x5u');
    }

    const badHost = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', x5u: 'https://evil.example.org/v1/keys/cert.pem' },
      registry,
    );
    assert.equal((badHost as { reason: string }).reason, 'invalid_x5u');

    const both = await resolveKeySource(
      { kid: 'tenant-a', alg: 'ES256', jku: validJku, x5u: validJku.replace('.jwk', '.pem') },
      registry,
    );
    assert.equal((both as { reason: string }).reason, 'multiple_key_urls');
  });
});
