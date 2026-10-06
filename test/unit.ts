import { PolicyEngine } from '../src/engine';
import { PolicyConfig } from '../src/types';
import { test, assert, assertEqual } from './harness';

function engineWith(config: PolicyConfig): PolicyEngine {
  const engine = new PolicyEngine();
  const result = engine.update(config);
  assert(result.ok === true, 'config should be accepted');
  return engine;
}

test('more specific prefix wins over less specific', () => {
  const engine = engineWith({
    groups: {},
    rules: [
      { id: 'r1', subject: 'alice', subjectKind: 'user', prefix: '/repo', effect: 'deny' },
      { id: 'r2', subject: 'alice', subjectKind: 'user', prefix: '/repo/docs', effect: 'allow' },
    ],
  });
  const decision = engine.decide('alice', '/repo/docs/readme.md');
  assertEqual(decision.effect, 'allow', 'effect');
  assertEqual(decision.effective && decision.effective.id, 'r2', 'effective rule');
  assertEqual(decision.shadowed.map((r) => r.id), ['r1'], 'shadowed rules');
});

test('deny wins at the same depth (same-level conflict)', () => {
  const engine = engineWith({
    groups: {},
    rules: [
      { id: 'allow-a', subject: 'alice', subjectKind: 'user', prefix: '/repo', effect: 'allow' },
      { id: 'deny-a', subject: 'alice', subjectKind: 'user', prefix: '/repo', effect: 'deny' },
    ],
  });
  const decision = engine.decide('alice', '/repo/x');
  assertEqual(decision.effect, 'deny', 'effect');
  assertEqual(decision.effective && decision.effective.id, 'deny-a', 'effective rule');
  assertEqual(decision.shadowed.map((r) => r.id), ['allow-a'], 'shadowed rules');
});

test('no matching rule defaults to deny', () => {
  const engine = engineWith({
    groups: {},
    rules: [
      { id: 'r1', subject: 'alice', subjectKind: 'user', prefix: '/repo', effect: 'allow' },
    ],
  });
  const other = engine.decide('alice', '/etc/passwd');
  assertEqual(other.effect, 'deny', 'unmatched path effect');
  assertEqual(other.defaultDeny, true, 'defaultDeny flag');
  assertEqual(other.effective, null, 'no effective rule');
  const stranger = engine.decide('bob', '/repo');
  assertEqual(stranger.effect, 'deny', 'unmatched user effect');
  assertEqual(stranger.defaultDeny, true, 'defaultDeny flag for user');
});

test('prefix matching is segment-aware', () => {
  const engine = engineWith({
    groups: {},
    rules: [
      { id: 'r1', subject: 'alice', subjectKind: 'user', prefix: '/repo', effect: 'allow' },
    ],
  });
  assertEqual(engine.decide('alice', '/repo').effect, 'allow', 'exact match');
  assertEqual(engine.decide('alice', '/repo/a').effect, 'allow', 'child path');
  assertEqual(engine.decide('alice', '/repository').effect, 'deny', 'not a segment prefix');
});

test('group inheritance applies transitively', () => {
  const engine = engineWith({
    groups: {
      eng: { members: ['alice'] },
      staff: { groups: ['eng'] },
      everyone: { groups: ['staff'], members: ['carol'] },
    },
    rules: [
      { id: 'g1', subject: 'everyone', subjectKind: 'group', prefix: '/app', effect: 'allow' },
    ],
  });
  assertEqual(engine.groupsOf('alice'), ['eng', 'everyone', 'staff'], 'transitive groups');
  assertEqual(engine.decide('alice', '/app/x').effect, 'allow', 'inherited allow');
  assertEqual(engine.decide('carol', '/app/x').effect, 'allow', 'direct member allow');
  assertEqual(engine.decide('mallory', '/app/x').effect, 'deny', 'non-member denied');
});

test('inheritance cycle rejects the update and keeps the old config', () => {
  const engine = engineWith({
    groups: { a: { members: ['alice'] } },
    rules: [
      { id: 'r1', subject: 'a', subjectKind: 'group', prefix: '/x', effect: 'allow' },
    ],
  });
  const result = engine.update({
    groups: {
      a: { groups: ['b'] },
      b: { groups: ['c'] },
      c: { groups: ['a'] },
    },
    rules: [
      { id: 'r2', subject: 'a', subjectKind: 'group', prefix: '/x', effect: 'deny' },
    ],
  });
  assert(result.ok === false, 'cyclic update must be rejected');
  if (!result.ok) {
    assertEqual(result.errors[0] && result.errors[0].kind, 'inheritance-cycle', 'error kind');
  }
  const decision = engine.decide('alice', '/x');
  assertEqual(decision.effect, 'allow', 'old config still in effect');
  assertEqual(decision.effective && decision.effective.id, 'r1', 'old rule still effective');
});

test('invalid configs are rejected atomically', () => {
  const engine = engineWith({ groups: {}, rules: [] });
  const dup = engine.update({
    groups: {},
    rules: [
      { id: 'r', subject: 'u', subjectKind: 'user', prefix: '/a', effect: 'allow' },
      { id: 'r', subject: 'u', subjectKind: 'user', prefix: '/b', effect: 'deny' },
    ],
  });
  assert(dup.ok === false, 'duplicate rule ids rejected');
  const unknown = engine.update({
    groups: {},
    rules: [
      { id: 'r', subject: 'ghost', subjectKind: 'group', prefix: '/a', effect: 'allow' },
    ],
  });
  assert(unknown.ok === false, 'unknown group subject rejected');
});

test('revocation: removing a rule reverts the decision', () => {
  const engine = new PolicyEngine();
  const withAllow: PolicyConfig = {
    groups: { eng: { members: ['alice'] } },
    rules: [
      { id: 'r1', subject: 'eng', subjectKind: 'group', prefix: '/repo', effect: 'allow' },
    ],
  };
  assert(engine.update(withAllow).ok === true, 'initial update');
  assertEqual(engine.decide('alice', '/repo').effect, 'allow', 'allowed before revoke');
  const revoked = engine.update({ groups: withAllow.groups, rules: [] });
  assert(revoked.ok === true, 'revoking update');
  const after = engine.decide('alice', '/repo');
  assertEqual(after.effect, 'deny', 'denied after revoke');
  assertEqual(after.defaultDeny, true, 'falls back to default deny');
});

test('shadowed rules report covers deeper, equal and shallower matches', () => {
  const engine = engineWith({
    groups: { eng: { members: ['alice'] } },
    rules: [
      { id: 'shallow', subject: 'eng', subjectKind: 'group', prefix: '/', effect: 'deny' },
      { id: 'mid', subject: 'alice', subjectKind: 'user', prefix: '/a/b', effect: 'allow' },
      { id: 'deep', subject: 'eng', subjectKind: 'group', prefix: '/a/b/c', effect: 'allow' },
    ],
  });
  const decision = engine.decide('alice', '/a/b/c/d');
  assertEqual(decision.effective && decision.effective.id, 'deep', 'deepest wins');
  assertEqual(
    decision.shadowed.map((r) => r.id),
    ['mid', 'shallow'],
    'shadowed ordered by precedence',
  );
});
