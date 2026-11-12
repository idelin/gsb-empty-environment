import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { evaluate, externalSecret, SELF } from './helpers.ts';
import type { ExistingTargetSecret } from '../src/types.ts';

function target(
  overrides: Partial<ExistingTargetSecret> = {},
): ExistingTargetSecret {
  return {
    name: 'app-secret',
    namespace: 'team-a',
    ownerReferences: [],
    data: {},
    ...overrides,
  };
}

describe('Owner 策略目标 Secret 属主隔离', () => {
  it('目标 Secret 已有他人属主时拒绝接管', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Owner' }),
      existingTarget: target({
        ownerReferences: [
          {
            apiVersion: 'external-secrets.io/v1',
            kind: 'ExternalSecret',
            name: 'victim-es',
            uid: 'uid-victim-0002',
          },
        ],
      }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'FOREIGN_OWNER');
    assert.equal(decision.denials[0]?.details?.ownerName, 'victim-es');
  });

  it('属主为本 ExternalSecret 时放行刷新', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Owner' }),
      existingTarget: target({ ownerReferences: [SELF] }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
  });

  it('目标 Secret 无属主时放行（可建立属主关系）', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Owner' }),
      existingTarget: target(),
    });

    assert.equal(decision.action, 'apply');
  });

  it('目标尚不存在时放行创建', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Owner' }),
      existingTarget: undefined,
    });

    assert.equal(decision.action, 'apply');
  });

  it('Orphan 策略不检查属主，他人属主也允许写入', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ creationPolicy: 'Orphan' }),
      existingTarget: target({
        ownerReferences: [
          {
            apiVersion: 'v1',
            kind: 'ConfigMap',
            name: 'other-controller',
            uid: 'uid-other-0003',
          },
        ],
      }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
  });
});
