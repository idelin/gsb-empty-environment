import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { PolicyEngine, EvidenceMissingError } from '../src/engine.ts';
import { ModuleRegistry, RegistryError } from '../src/registry.ts';
import type { ModuleSet } from '../src/policy.ts';

const v0: ModuleSet = {
  root: 'main',
  modules: {
    main: {
      id: 'main',
      combining: 'permit-overrides',
      rules: [
        {
          id: 'r1',
          effect: 'Permit',
          condition: { op: 'in', attr: 'role', values: ['admin', 'auditor'] },
          obligations: [{ kind: 'quota', id: 'daily', limit: 100 }],
        },
      ],
    },
  },
};

const v1: ModuleSet = {
  root: 'main',
  modules: {
    main: {
      id: 'main',
      combining: 'deny-overrides',
      rules: [
        {
          id: 'r1',
          effect: 'Permit',
          condition: { op: 'in', attr: 'role', values: ['admin'] },
          obligations: [{ kind: 'quota', id: 'daily', limit: 10 }],
        },
        { id: 'r2', effect: 'Deny', condition: { op: 'gte', attr: 'risk', value: 80 } },
      ],
    },
  },
};

describe('模块版本原子切换', () => {
  it('更新后新版本立即生效，旧版本仍可重放', () => {
    const registry = new ModuleRegistry(v0);
    const engine = new PolicyEngine(registry);

    const before = engine.evaluate({ role: 'auditor', risk: 10 });
    assert.equal(before.decision, 'Permit');
    const decisionId = engine.lastDecisionId();

    registry.update(v1);
    const after = engine.evaluate({ role: 'auditor', risk: 10 });
    assert.equal(after.decision, 'NotApplicable');

    const replay = engine.replay(decisionId);
    assert.equal(replay.version, 0);
    assert.equal(replay.replayed.decision, 'Permit');
    assert.deepEqual(replay.echoedAttributes, { role: 'auditor', risk: 10 });
  });

  it('非法更新（模块环）被拒绝，当前版本保持不变', () => {
    const registry = new ModuleRegistry(v0);
    const ring: ModuleSet = {
      root: 'a',
      modules: {
        a: { id: 'a', combining: 'permit-overrides', rules: [], moduleRefs: ['b'] },
        b: { id: 'b', combining: 'permit-overrides', rules: [], moduleRefs: ['a'] },
      },
    };
    assert.throws(() => registry.update(ring), RegistryError);
    assert.equal(registry.currentVersion, 0);
    assert.equal(registry.current, registry.at(0));
  });

  it('自引用模块环同样被拒绝', () => {
    const selfRing: ModuleSet = {
      root: 'a',
      modules: { a: { id: 'a', combining: 'permit-overrides', rules: [], moduleRefs: ['a'] } },
    };
    assert.throws(() => new ModuleRegistry(selfRing), /module ring detected/);
  });

  it('引用未知模块被拒绝', () => {
    const dangling: ModuleSet = {
      root: 'a',
      modules: { a: { id: 'a', combining: 'permit-overrides', rules: [], moduleRefs: ['ghost'] } },
    };
    assert.throws(() => new ModuleRegistry(dangling), /unknown module/);
  });
});

describe('解释证据缺失', () => {
  it('重放不存在的决策 => EvidenceMissingError', () => {
    const engine = new PolicyEngine(new ModuleRegistry(v0));
    assert.throws(() => engine.replay('d999'), EvidenceMissingError);
  });

  it('证据被清除后重放失败', () => {
    const engine = new PolicyEngine(new ModuleRegistry(v0));
    engine.evaluate({ role: 'admin' });
    const id = engine.lastDecisionId();
    engine.dropEvidence(id);
    assert.throws(() => engine.replay(id), /interpretation evidence missing/);
  });
});

describe('属性求值边界', () => {
  it('缺失属性 => Indeterminate，不外呼身份服务', () => {
    const engine = new PolicyEngine(new ModuleRegistry(v1));
    const r = engine.evaluate({ role: 'admin' });
    assert.equal(r.decision, 'Indeterminate');
  });

  it('deny-overrides: 高风险拒绝覆盖允许', () => {
    const engine = new PolicyEngine(new ModuleRegistry(v1));
    const r = engine.evaluate({ role: 'admin', risk: 90 });
    assert.equal(r.decision, 'Deny');
  });
});
