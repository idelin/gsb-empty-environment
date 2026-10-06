import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PolicyEngine } from '../src/index.ts';
import type { PolicyConfig } from '../src/index.ts';

test('deeper prefix wins over shallower one', () => {
  const engine = new PolicyEngine({
    groups: {
      g: {
        rules: [
          { prefix: '/a', effect: 'allow' },
          { prefix: '/a/b', effect: 'deny' },
        ],
      },
    },
  });
  assert.equal(engine.decide('g', '/a/b/c').effect, 'deny');
  assert.equal(engine.decide('g', '/a/b').effect, 'deny');
  assert.equal(engine.decide('g', '/a/c').effect, 'allow');
  assert.equal(engine.decide('g', '/a').effect, 'allow');
});

test('same depth: deny wins (same-level conflict)', () => {
  const engine = new PolicyEngine({
    groups: {
      g: {
        rules: [
          { prefix: '/x', effect: 'allow' },
          { prefix: '/x', effect: 'deny' },
        ],
      },
    },
  });
  const d = engine.decide('g', '/x/anything');
  assert.equal(d.effect, 'deny');
  assert.equal(d.winner?.rule.effect, 'deny');

  const explanation = engine.explain('g');
  assert.equal(explanation.effective.length, 1);
  assert.equal(explanation.shadowed.length, 1);
  assert.equal(explanation.shadowed[0].reason, 'deny-wins-same-prefix');
  assert.equal(explanation.shadowed[0].shadowed.rule.effect, 'allow');
});

test('no matching rule defaults to deny', () => {
  const engine = new PolicyEngine({
    groups: { g: { rules: [{ prefix: '/only/this', effect: 'allow' }] } },
  });
  const d = engine.decide('g', '/other');
  assert.equal(d.effect, 'deny');
  assert.equal(d.reason, 'default-deny');
  assert.equal(d.winner, null);
});

test('root prefix matches everything; segment boundary respected', () => {
  const engine = new PolicyEngine({
    groups: {
      g: {
        rules: [
          { prefix: '/', effect: 'allow' },
          { prefix: '/a', effect: 'deny' },
        ],
      },
    },
  });
  assert.equal(engine.decide('g', '/anything/at/all').effect, 'allow');
  assert.equal(engine.decide('g', '/a').effect, 'deny');
  // '/ab' is not under '/a'
  assert.equal(engine.decide('g', '/ab').effect, 'allow');
});

test('group inheritance: child sees parent rules, deeper child rule overrides', () => {
  const config: PolicyConfig = {
    groups: {
      base: { rules: [{ prefix: '/data', effect: 'allow' }] },
      child: {
        inherits: ['base'],
        rules: [{ prefix: '/data/secret', effect: 'deny' }],
      },
    },
  };
  const engine = new PolicyEngine(config);
  assert.equal(engine.decide('child', '/data/public').effect, 'allow');
  assert.equal(engine.decide('child', '/data/secret/x').effect, 'deny');
  assert.equal(engine.decide('base', '/data/secret/x').effect, 'allow');
});

test('diamond inheritance expands each rule once', () => {
  const engine = new PolicyEngine({
    groups: {
      top: { rules: [{ prefix: '/t', effect: 'allow' }] },
      left: { inherits: ['top'] },
      right: { inherits: ['top'] },
      bottom: { inherits: ['left', 'right'] },
    },
  });
  const explanation = engine.explain('bottom');
  assert.equal(explanation.effective.length, 1);
  assert.equal(explanation.effective[0].origin, 'top');
  assert.equal(engine.decide('bottom', '/t').effect, 'allow');
});

test('inherited duplicate is shadowed by the group own rule', () => {
  const engine = new PolicyEngine({
    groups: {
      base: { rules: [{ prefix: '/p', effect: 'allow' }] },
      child: {
        inherits: ['base'],
        rules: [{ prefix: '/p', effect: 'allow' }],
      },
    },
  });
  const explanation = engine.explain('child');
  assert.equal(explanation.effective.length, 1);
  assert.equal(explanation.effective[0].origin, 'child');
  assert.equal(explanation.shadowed.length, 1);
  assert.equal(explanation.shadowed[0].reason, 'duplicate');
  assert.equal(explanation.shadowed[0].shadowed.origin, 'base');
});

test('inheritance cycle rejects the update and keeps the old config (atomic)', () => {
  const engine = new PolicyEngine({
    groups: { g: { rules: [{ prefix: '/ok', effect: 'allow' }] } },
  });
  const cyclic: PolicyConfig = {
    groups: {
      a: { inherits: ['c'], rules: [{ prefix: '/a', effect: 'allow' }] },
      b: { inherits: ['a'] },
      c: { inherits: ['b'] },
    },
  };
  const result = engine.update(cyclic);
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error.kind, 'inheritance-cycle');
    const cycle = (result.error as { cycle: string[] }).cycle;
    assert.equal(cycle[0], cycle[cycle.length - 1]);
    assert.ok(cycle.length >= 3);
  }
  // 撤销: rejected update leaves the previous config fully in effect.
  assert.equal(engine.decide('g', '/ok').effect, 'allow');
  assert.equal(engine.decide('a', '/a').reason, 'unknown-group');
  assert.deepEqual(engine.snapshot(), {
    groups: { g: { rules: [{ prefix: '/ok', effect: 'allow' }] } },
  });
});

test('self-inheritance is a cycle', () => {
  const engine = new PolicyEngine();
  const result = engine.update({ groups: { a: { inherits: ['a'] } } });
  assert.equal(result.ok, false);
  if (!result.ok) assert.equal(result.error.kind, 'inheritance-cycle');
});

test('unknown inherited group rejects the update', () => {
  const engine = new PolicyEngine({
    groups: { g: { rules: [{ prefix: '/ok', effect: 'allow' }] } },
  });
  const result = engine.update({ groups: { g: { inherits: ['ghost'] } } });
  assert.equal(result.ok, false);
  if (!result.ok) {
    assert.equal(result.error.kind, 'unknown-group');
  }
  assert.equal(engine.decide('g', '/ok').effect, 'allow');
});

test('invalid rule shapes reject the update', () => {
  const engine = new PolicyEngine();
  const bad1 = engine.update({ groups: { g: { rules: [{ prefix: '/x', effect: 'maybe' } as never] } } });
  assert.equal(bad1.ok, false);
  const bad2 = engine.update({ groups: { g: { rules: [{ effect: 'allow' } as never] } } });
  assert.equal(bad2.ok, false);
});

test('one update atomically replaces the whole config', () => {
  const engine = new PolicyEngine({
    groups: { old: { rules: [{ prefix: '/a', effect: 'allow' }] } },
  });
  const result = engine.update({
    groups: { fresh: { rules: [{ prefix: '/b', effect: 'allow' }] } },
  });
  assert.equal(result.ok, true);
  assert.equal(engine.decide('old', '/a').reason, 'unknown-group');
  assert.equal(engine.decide('fresh', '/b').effect, 'allow');
  assert.equal(engine.decide('fresh', '/a').reason, 'default-deny');
});

test('constructor rejects an invalid initial config', () => {
  assert.throws(
    () => new PolicyEngine({ groups: { a: { inherits: ['a'] } } }),
    /inheritance cycle/,
  );
});

test('prefixes are normalized (slashes, trailing slash, case preserved)', () => {
  const engine = new PolicyEngine({
    groups: { g: { rules: [{ prefix: 'data//reports/', effect: 'allow' }] } },
  });
  assert.equal(engine.decide('g', '/data/reports/q1').effect, 'allow');
  assert.equal(engine.decide('g', 'data/reports').effect, 'allow');
  assert.equal(engine.decide('g', '/Data/reports').effect, 'deny');
});
