import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { parseProtectedKeys } from '../src/ownership.ts';
import { evaluate, externalSecret, refresh } from './helpers.ts';
import type { ExistingTargetSecret } from '../src/types.ts';

function mergeSecret(
  overrides: Partial<ExistingTargetSecret> = {},
): ExistingTargetSecret {
  return {
    name: 'app-secret',
    namespace: 'team-a',
    ownerReferences: [],
    data: { token: 'old-token', keep: 'keep-me' },
    ...overrides,
  };
}

describe('Merge 策略保护标签覆盖判定', () => {
  it('拟覆盖逗号列表声明的受保护键时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Merge' }),
      existingTarget: mergeSecret({
        labels: {
          'eso.example.com/protected-keys': 'token, keep',
        },
      }),
      refresh: refresh({ fetched: { token: 'new-token' } }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'PROTECTED_KEY_OVERWRITE');
    assert.deepEqual(decision.denials[0]?.details?.protectedKeys, ['token']);
  });

  it('拟覆盖单键保护标签声明的键时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Merge' }),
      existingTarget: mergeSecret({
        labels: {
          'eso.example.com/protected-key/token': 'true',
        },
      }),
      refresh: refresh({ fetched: { token: 'new-token' } }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'PROTECTED_KEY_OVERWRITE');
  });

  it('只写入未受保护的新键时放行并保留旧数据规划', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Merge' }),
      existingTarget: mergeSecret({
        labels: {
          'eso.example.com/protected-keys': 'token',
        },
      }),
      refresh: refresh({ fetched: { 'team-a/new-key': 'new-value' } }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
    assert.deepEqual(decision.plannedData, {
      'team-a/new-key': 'new-value',
    });
  });

  it('无保护标签时 Merge 允许覆盖现有键', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Merge' }),
      existingTarget: mergeSecret(),
      refresh: refresh({ fetched: { token: 'new-token' } }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.plannedData.token, 'new-token');
  });

  it('parseProtectedKeys 同时识别两种标签形式', () => {
    const protectedKeys = parseProtectedKeys({
      'eso.example.com/protected-keys': 'a, b',
      'eso.example.com/protected-key/c': 'true',
      'unrelated-label': '1',
    });

    assert.deepEqual([...protectedKeys].sort(), ['a', 'b', 'c']);
  });
});
