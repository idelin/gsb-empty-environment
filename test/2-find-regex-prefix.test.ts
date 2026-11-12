import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { buildPrefixEscapeProbes } from '../src/keys.ts';
import { evaluate, externalSecret } from './helpers.ts';

describe('find.name 正则越前缀判定', () => {
  it('未锚定正则可匹配其他前缀的键时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        dataFrom: [{ find: { name: 'password' } }],
      }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'FIND_REGEX_PREFIX_ESCAPE');
  });

  it('以允许前缀锚定的正则放行', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        dataFrom: [{ find: { name: '^team-a/' } }],
      }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
  });

  it('显式 key 超出允许前缀时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        dataFrom: [{ key: 'team-b/db/password' }],
      }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'KEY_PREFIX_VIOLATION');
  });

  it('正则无法编译时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        dataFrom: [{ find: { name: '(' } }],
      }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'FIND_REGEX_INVALID');
  });

  it('运行时实际匹配到越前缀键时拒绝（即使静态探针未命中）', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        dataFrom: [{ find: { name: '^team-[ab]/' } }],
      }),
      providerKeys: ['team-a/ok', 'team-b/stolen'],
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'FIND_MATCHED_DISALLOWED_KEY');
    assert.deepEqual(
      decision.denials[0]?.details?.disallowedKeys,
      ['team-b/stolen'],
    );
  });

  it('探针本身不得落在允许前缀内，否则判定失效', () => {
    const prefixes = ['team-a/'];
    for (const probe of buildPrefixEscapeProbes(prefixes)) {
      assert.equal(
        prefixes.some((prefix) => probe.startsWith(prefix)),
        false,
        `探针 ${probe} 不应命中允许前缀`,
      );
    }
  });
});
