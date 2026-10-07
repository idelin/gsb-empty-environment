import assert from 'node:assert/strict';
import { test } from './harness';
import {
  and,
  canonicalize,
  checkOrderIndependence,
  custom,
  eq,
  evaluate,
  evaluateNaive,
  gt,
  renderEvidence,
  Rule,
  Snapshot,
} from '../src';

test('higher priority wins per field', () => {
  const rules: Rule[] = [
    { id: 'low', priority: 1, condition: eq('kind', 'a'), assigns: { out: 'low' } },
    { id: 'high', priority: 10, condition: eq('kind', 'a'), assigns: { out: 'high' } },
  ];
  const result = evaluate(rules, { kind: 'a' });
  assert.equal(result.output['out'], 'high');
  const field = result.fields.find((f) => f.field === 'out')!;
  assert.equal(field.status, 'resolved');
  assert.equal(field.winner!.ruleId, 'high');
  const outcomes = Object.fromEntries(field.candidates.map((c) => [c.ruleId, c.outcome]));
  assert.deepEqual(outcomes, { high: 'winner', low: 'shadowed' });
});

test('same priority with different values is a conflict; snapshot value stands', () => {
  const rules: Rule[] = [
    { id: 'r1', priority: 5, condition: eq('go', true), assigns: { x: 1 } },
    { id: 'r2', priority: 5, condition: eq('go', true), assigns: { x: 2 } },
    { id: 'r3', priority: 1, condition: eq('go', true), assigns: { x: 3 } },
  ];
  const result = evaluate(rules, { go: true, x: 0 });
  assert.equal(result.output['x'], 0, 'conflicted field keeps snapshot value');
  assert.equal(result.conflicts.length, 1);
  assert.equal(result.conflicts[0]!.field, 'x');
  assert.equal(result.conflicts[0]!.priority, 5);
  assert.deepEqual(
    result.conflicts[0]!.entries.map((e) => e.ruleId),
    ['r1', 'r2'],
  );
  const field = result.fields.find((f) => f.field === 'x')!;
  assert.equal(field.status, 'conflict');
  const outcomes = Object.fromEntries(field.candidates.map((c) => [c.ruleId, c.outcome]));
  assert.deepEqual(outcomes, { r1: 'conflict', r2: 'conflict', r3: 'shadowed' });
});

test('same priority with equal values agrees and resolves', () => {
  const rules: Rule[] = [
    { id: 'a', priority: 3, condition: eq('go', true), assigns: { x: [1, 2] } },
    { id: 'b', priority: 3, condition: eq('go', true), assigns: { x: [1, 2] } },
  ];
  const result = evaluate(rules, { go: true });
  assert.deepEqual(result.output['x'], [1, 2]);
  assert.equal(result.conflicts.length, 0);
  const field = result.fields.find((f) => f.field === 'x')!;
  const outcomes = field.candidates.map((c) => c.outcome).sort();
  assert.deepEqual(outcomes, ['agreed', 'winner']);
});

test('rules cannot read assignments made in the same round', () => {
  const rules: Rule[] = [
    { id: 'writer', priority: 1, condition: eq('x', 0), assigns: { x: 1 } },
    // Reads x: sees the snapshot value 0, not writer's 1.
    { id: 'reader-new', priority: 5, condition: eq('x', 1), assigns: { y: 'saw-new' } },
    { id: 'reader-old', priority: 5, condition: eq('x', 0), assigns: { y: 'saw-old' } },
  ];
  const result = evaluate(rules, { x: 0 });
  assert.equal(result.output['x'], 1, 'writer still applies');
  assert.equal(result.output['y'], 'saw-old', 'reader observes snapshot, not the write');
  const matched = Object.fromEntries(result.rules.map((r) => [r.ruleId, r.matched]));
  assert.deepEqual(matched, { writer: true, 'reader-new': false, 'reader-old': true });
});

test('unmatched rules contribute nothing; untouched fields pass through', () => {
  const rules: Rule[] = [
    { id: 'off', priority: 9, condition: eq('mode', 'on'), assigns: { x: 1 } },
  ];
  const result = evaluate(rules, { mode: 'off', keep: 'me' });
  assert.equal(result.output['keep'], 'me');
  assert.equal('x' in result.output, false);
  const field = result.fields.find((f) => f.field === 'keep')!;
  assert.equal(field.status, 'untouched');
});

test('composite conditions evaluate against the snapshot with explainable evidence', () => {
  const rules: Rule[] = [
    { id: 'combo', priority: 1, condition: and(gt('age', 18), eq('vip', true)), assigns: { tier: 'gold' } },
  ];
  const result = evaluate(rules, { age: 20, vip: false });
  assert.equal(result.rules[0]!.matched, false);
  const evidence = result.rules[0]!.condition;
  assert.equal(evidence.kind, 'and');
  assert.equal(evidence.children!.length, 2);
  assert.equal(evidence.children![0]!.result, true);
  assert.equal(evidence.children![1]!.result, false);
});

test('evidence tree renders per field with candidates', () => {
  const rules: Rule[] = [
    { id: 'r1', priority: 2, condition: eq('k', 1), assigns: { x: 'a' } },
    { id: 'r2', priority: 4, condition: eq('k', 1), assigns: { x: 'b' } },
  ];
  const text = renderEvidence(evaluate(rules, { k: 1 }));
  assert.ok(text.includes('x: "b"  <- r2 (priority 4)'));
  assert.ok(text.includes('[winner] r2 priority=4 value="b"'));
  assert.ok(text.includes('[shadowed] r1 priority=2 value="a"'));
});

test('engine matches naive two-phase reference on randomized rule sets', () => {
  // Deterministic pseudo-random generator.
  let seed = 42;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let round = 0; round < 50; round++) {
    const snapshot: Record<string, unknown> = {
      s0: Math.floor(rand() * 4),
      s1: Math.floor(rand() * 4),
    };
    const rules: Rule[] = [];
    const n = 1 + Math.floor(rand() * 6);
    for (let i = 0; i < n; i++) {
      const field = `f${Math.floor(rand() * 3)}`;
      const condField = `s${Math.floor(rand() * 2)}`;
      rules.push({
        id: `rule-${i}`,
        priority: Math.floor(rand() * 4),
        condition: eq(condField, Math.floor(rand() * 4)),
        assigns: { [field]: Math.floor(rand() * 3) },
      });
    }
    const fast = canonicalize(evaluate(rules, snapshot));
    const naive = canonicalize(evaluateNaive(rules, snapshot));
    assert.equal(fast, naive, `round ${round} diverged`);
  }
});

test('order-independence checker confirms shuffled configs agree', () => {
  const rules: Rule[] = [
    { id: 'a', priority: 2, condition: eq('k', 1), assigns: { x: 'a', y: 1 } },
    { id: 'b', priority: 2, condition: eq('k', 1), assigns: { x: 'b' } },
    { id: 'c', priority: 5, condition: eq('k', 1), assigns: { y: 2 } },
    { id: 'd', priority: 1, condition: custom('never', () => false), assigns: { z: 0 } },
    { id: 'e', priority: 5, condition: eq('k', 1), assigns: { y: 2 } },
  ];
  const snapshot: Snapshot = { k: 1, x: 'orig' };
  const report = checkOrderIndependence(rules, snapshot);
  assert.equal(report.independent, true);
  assert.equal(report.permutationsChecked, 120, '5 rules -> 5! permutations');
});

test('order-independence checker samples large rule sets', () => {
  const rules: Rule[] = [];
  for (let i = 0; i < 12; i++) {
    rules.push({
      id: `r${i}`,
      priority: i % 3,
      condition: eq('k', 1),
      assigns: { [`f${i % 4}`]: i },
    });
  }
  const report = checkOrderIndependence(rules, { k: 1 }, { maxPermutations: 64 });
  assert.equal(report.independent, true);
  assert.equal(report.permutationsChecked, 64);
});

test('order-independence checker catches stateful conditions', () => {
  // Each rule evaluation consumes two predicate calls (evaluate + explain),
  // so a predicate whose result depends on call parity across rounds is
  // genuinely order-dependent and must be flagged.
  let calls = 0;
  const stateful = custom('call-count', () => {
    calls += 1;
    return calls % 4 === 1;
  });
  const rules: Rule[] = [
    { id: 'stateful', priority: 1, condition: stateful, assigns: { x: 1 } },
    { id: 'plain', priority: 1, condition: eq('k', 1), assigns: { y: 1 } },
  ];
  const report = checkOrderIndependence(rules, { k: 1 });
  assert.equal(report.independent, false, 'a condition reading mutable state breaks determinism');
});
