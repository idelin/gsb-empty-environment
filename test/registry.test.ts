import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ModuleRegistry, type PolicyModule } from '../src/registry.ts';
import type { AttributeEnvelope } from '../src/attributes.ts';

function moduleV1(): PolicyModule {
  return {
    name: 'access',
    version: 1,
    combiner: 'deny-overrides',
    descriptors: [
      { name: 'age', type: 'integer' },
      { name: 'roles', type: 'string-set' },
    ],
    policies: [
      {
        id: 'p1',
        combiner: 'first-applicable',
        rules: [
          { id: 'deny-minors', effect: 'Deny', condition: { op: 'int-lt', attr: 'age', value: 18 } },
          {
            id: 'permit-admins',
            effect: 'Permit',
            condition: { op: 'contains', attr: 'roles', member: 'admin' },
            obligations: [
              { kind: 'quota', resource: 'api', limit: 100 },
              { kind: 'field-mask', fields: ['ssn'] },
            ],
          },
        ],
      },
    ],
  };
}

function moduleV2(): PolicyModule {
  return {
    name: 'access',
    version: 2,
    combiner: 'deny-overrides',
    descriptors: [
      { name: 'subject.age', type: 'integer' },
      { name: 'subject.roles', type: 'string-set' },
    ],
    policies: [
      {
        id: 'p1',
        combiner: 'first-applicable',
        rules: [
          { id: 'deny-minors', effect: 'Deny', condition: { op: 'int-lt', attr: 'subject.age', value: 18 } },
          {
            id: 'permit-admins',
            effect: 'Permit',
            condition: { op: 'contains', attr: 'subject.roles', member: 'admin' },
            obligations: [
              { kind: 'quota', resource: 'api', limit: 50 },
              { kind: 'field-mask', fields: ['ssn', 'email'] },
            ],
          },
        ],
      },
    ],
  };
}

test('旧版本属性回包：v1 回包求值、原子切换到 v2、历史决策按当时版本重放', () => {
  const registry = new ModuleRegistry();
  registry.register(moduleV1());
  registry.register(moduleV2());

  assert.deepEqual(registry.activate('access', 1), { ok: true });

  const envelopeV1 = { version: 1, attributes: { age: 30, roles: ['admin', 'dev'] } } as const;
  const snapshot = structuredClone(envelopeV1);
  const record = registry.evaluate('access', envelopeV1);

  assert.equal(record.moduleVersion, 1);
  assert.equal(record.outcome.decision, 'Permit');
  assert.deepEqual(record.outcome.obligations, [
    { kind: 'quota', resource: 'api', limit: 100 },
    { kind: 'field-mask', fields: ['ssn'] },
  ]);
  assert.deepEqual(record.outcome.accessed, ['age', 'roles']);
  // 策略只读：回包在求值后保持原样
  assert.deepEqual(envelopeV1, snapshot);

  // 原子切换到 v2：切换前后 activeVersion 不会停留在中间态
  assert.deepEqual(registry.activate('access', 2), { ok: true });
  assert.equal(registry.activeVersion('access'), 2);

  const envelopeV2: AttributeEnvelope = {
    version: 2,
    scopes: { subject: { age: 30, roles: ['admin'] } },
  };
  const record2 = registry.evaluate('access', envelopeV2);
  assert.equal(record2.moduleVersion, 2);
  assert.equal(record2.outcome.decision, 'Permit');
  assert.deepEqual(record2.outcome.obligations, [
    { kind: 'quota', resource: 'api', limit: 50 },
    { kind: 'field-mask', fields: ['email', 'ssn'] },
  ]);

  // 历史决策用记录时的模块版本与原始回包重放，结果逐位一致
  const replay = registry.replay(record.id);
  assert.ok(replay.match);
  assert.deepEqual(replay.replayed, record.outcome);
  assert.equal(registry.activeVersion('access'), 2, '重放不得改变当前激活版本');
});

test('模块环：激活时检测委托环并原子拒绝，已激活版本不受影响', () => {
  const registry = new ModuleRegistry();
  const a: PolicyModule = {
    name: 'A', version: 1, combiner: 'deny-overrides',
    policies: [], descriptors: [], delegates: ['B'],
  };
  const b: PolicyModule = {
    name: 'B', version: 1, combiner: 'deny-overrides',
    policies: [], descriptors: [], delegates: ['A'],
  };
  registry.register(a);
  registry.register(b);

  assert.deepEqual(registry.activate('A', 1), { ok: true });

  const rejected = registry.activate('B', 1);
  assert.equal(rejected.ok, false);
  assert.equal(rejected.ok ? undefined : rejected.reason, 'module-ring');
  assert.deepEqual(rejected.ok ? undefined : rejected.cycle, ['A', 'B', 'A']);
  assert.equal(registry.activeVersion('B'), undefined, '失败激活不得留下部分状态');
  assert.equal(registry.activeVersion('A'), 1, '已激活版本保持不变');

  const self: PolicyModule = {
    name: 'C', version: 1, combiner: 'deny-overrides',
    policies: [], descriptors: [], delegates: ['C'],
  };
  registry.register(self);
  const selfRing = registry.activate('C', 1);
  assert.equal(selfRing.ok, false);
  assert.equal(selfRing.ok ? undefined : selfRing.reason, 'module-ring');
});

test('解释证据缺失：无描述符属性 → Indeterminate 并列出缺失证据；缺值单列', () => {
  const registry = new ModuleRegistry();
  registry.register({
    name: 'evidence',
    version: 1,
    combiner: 'deny-overrides',
    descriptors: [
      { name: 'age', type: 'integer' },
      { name: 'level', type: 'integer' },
    ],
    policies: [
      {
        id: 'p1',
        combiner: 'first-applicable',
        rules: [
          { id: 'needs-clearance', effect: 'Permit', condition: { op: 'contains', attr: 'clearance', member: 'top' } },
          { id: 'needs-level', effect: 'Permit', condition: { op: 'int-ge', attr: 'level', value: 5 } },
        ],
      },
    ],
  });
  assert.deepEqual(registry.activate('evidence', 1), { ok: true });

  const record = registry.evaluate('evidence', { version: 1, attributes: { age: 30 } });
  assert.equal(record.outcome.decision, 'Indeterminate');
  assert.deepEqual(record.outcome.missingEvidence, ['clearance']);
  assert.deepEqual(record.outcome.missingValues, ['level']);
});

test('委托模块提供解释证据：描述符经 delegates 导入后可正常求值', () => {
  const registry = new ModuleRegistry();
  registry.register({
    name: 'directory',
    version: 1,
    combiner: 'deny-overrides',
    policies: [],
    descriptors: [{ name: 'dept', type: 'string-set' }],
  });
  registry.register({
    name: 'app',
    version: 1,
    combiner: 'deny-overrides',
    descriptors: [],
    delegates: ['directory'],
    policies: [
      {
        id: 'p1',
        combiner: 'first-applicable',
        rules: [
          {
            id: 'eng-only',
            effect: 'Permit',
            condition: { op: 'contains', attr: 'dept', member: 'eng' },
            obligations: [{ kind: 'output-format', format: 'json' }],
          },
        ],
      },
    ],
  });
  assert.deepEqual(registry.activate('directory', 1), { ok: true });
  assert.deepEqual(registry.activate('app', 1), { ok: true });

  const record = registry.evaluate('app', { version: 1, attributes: { dept: ['eng'] } });
  assert.equal(record.outcome.decision, 'Permit');
  assert.deepEqual(record.outcome.obligations, [{ kind: 'output-format', format: 'json' }]);
});
