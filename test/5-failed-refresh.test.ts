import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { evaluate, externalSecret, refresh } from './helpers.ts';

describe('刷新失败与空值降级', () => {
  const existingData = {
    'team-a/db/password': 'previous-secret',
  };

  it('刷新失败时跳过写入，绝不以空数据替换旧值', () => {
    const decision = evaluate({
      externalSecret: externalSecret(),
      existingTarget: {
        name: 'app-secret',
        namespace: 'team-a',
        ownerReferences: [],
        data: existingData,
      },
      refresh: refresh({ failed: true, fetched: {} }),
    });

    assert.equal(decision.action, 'skip');
    assert.equal(decision.allowed, true);
    assert.equal(decision.denials.length, 0);
    assert.equal(decision.notices[0]?.code, 'REFRESH_FAILED');
    assert.deepEqual(decision.plannedData, {});
  });

  it('刷新成功但取到空值且旧值非空时剔除该键并提示', () => {
    const decision = evaluate({
      externalSecret: externalSecret(),
      existingTarget: {
        name: 'app-secret',
        namespace: 'team-a',
        ownerReferences: [],
        data: existingData,
      },
      refresh: refresh({
        fetched: {
          'team-a/db/password': '',
          'team-a/db/extra': 'fresh',
        },
      }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.notices[0]?.code, 'EMPTY_VALUE_DOWNGRADE');
    assert.deepEqual(decision.notices[0]?.details?.keys, [
      'team-a/db/password',
    ]);
    assert.deepEqual(decision.plannedData, {
      'team-a/db/extra': 'fresh',
    });
  });

  it('目标不存在时空值仍可写入（无旧值可保护）', () => {
    const decision = evaluate({
      externalSecret: externalSecret(),
      existingTarget: undefined,
      refresh: refresh({
        fetched: { 'team-a/db/password': '' },
      }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.plannedData['team-a/db/password'], '');
  });

  it('正常刷新时返回规划数据且无提示', () => {
    const decision = evaluate({
      externalSecret: externalSecret(),
      existingTarget: {
        name: 'app-secret',
        namespace: 'team-a',
        ownerReferences: [],
        data: existingData,
      },
      refresh: refresh({ fetched: { 'team-a/db/password': 'rotated' } }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.notices.length, 0);
    assert.deepEqual(decision.plannedData, {
      'team-a/db/password': 'rotated',
    });
  });

  it('creationPolicy 为 None 且目标缺失时跳过创建', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'None' }),
      existingTarget: undefined,
    });

    assert.equal(decision.action, 'skip');
    assert.equal(decision.notices[0]?.code, 'TARGET_MISSING_WITH_NONE_POLICY');
  });
});
