import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  clusterStore,
  evaluate,
  externalSecret,
  namespaceStore,
} from './helpers.ts';

describe('跨命名空间存储引用隔离', () => {
  it('拒绝引用其他命名空间的命名空间范围 SecretStore', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ namespace: 'attacker' }),
      store: namespaceStore({ namespace: 'team-a' }),
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'CROSS_NAMESPACE_STORE_REF');
    assert.deepEqual(decision.plannedData, {});
  });

  it('同命名空间的 SecretStore 引用放行', () => {
    const decision = evaluate({
      externalSecret: externalSecret({ namespace: 'team-a' }),
      store: namespaceStore({ namespace: 'team-a' }),
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
  });

  it('ClusterSecretStore 拒绝不在白名单内的调用命名空间', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        namespace: 'attacker',
        storeRefKind: 'ClusterSecretStore',
        storeRefName: 'platform-store',
      }),
      store: clusterStore(),
      namespaceLabels: { 'secrets/tenant': 'attacker' },
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'NAMESPACE_NOT_WHITELISTED');
  });

  it('ClusterSecretStore 在白名单但标签选择器不满足时拒绝', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        namespace: 'team-a',
        storeRefKind: 'ClusterSecretStore',
        storeRefName: 'platform-store',
      }),
      store: clusterStore(),
      namespaceLabels: { 'secrets/tenant': 'someone-else' },
    });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'NAMESPACE_LABELS_REJECTED');
  });

  it('ClusterSecretStore 白名单与标签同时满足时放行', () => {
    const decision = evaluate({
      externalSecret: externalSecret({
        namespace: 'team-a',
        storeRefKind: 'ClusterSecretStore',
        storeRefName: 'platform-store',
      }),
      store: clusterStore(),
      namespaceLabels: { 'secrets/tenant': 'team-a' },
    });

    assert.equal(decision.action, 'apply');
    assert.equal(decision.denials.length, 0);
  });

  it('存储不存在时拒绝，避免猜测不可见存储', () => {
    const decision = evaluate({ store: undefined });

    assert.equal(decision.action, 'deny');
    assert.equal(decision.denials[0]?.code, 'STORE_NOT_FOUND');
  });
});
