import { PolicyEngine } from '../src/engine';
import { ReferenceModel } from '../src/reference';
import { prefixDepth } from '../src/paths';
import { Decision, GroupDef, PolicyConfig, Rule } from '../src/types';
import { test, assert, assertEqual, prng } from './harness';

const USERS = ['alice', 'bob', 'carol', 'dave', 'erin', 'mallory'];
const SEGMENTS = ['repo', 'docs', 'src', 'app', 'api', 'sec', 'x'];

interface Rng {
  next(): number;
  int(n: number): number;
  pick<T>(items: T[]): T;
  chance(p: number): boolean;
}

function makeRng(seed: number): Rng {
  const rand = prng(seed);
  return {
    next: rand,
    int: (n) => Math.floor(rand() * n),
    pick: (items) => items[Math.floor(rand() * items.length)]!,
    chance: (p) => rand() < p,
  };
}

function randomPrefix(rng: Rng): string {
  const depth = rng.int(4); // 0..3 segments, 0 means root "/"
  if (depth === 0) return '/';
  let p = '';
  for (let i = 0; i < depth; i++) p += '/' + rng.pick(SEGMENTS);
  return p;
}

/** Random acyclic group graph: group i may only include groups j < i. */
function randomGroups(rng: Rng, count: number): Record<string, GroupDef> {
  const groups: Record<string, GroupDef> = {};
  for (let i = 0; i < count; i++) {
    const gid = 'g' + i;
    const def: GroupDef = {};
    const members = USERS.filter(() => rng.chance(0.25));
    if (members.length > 0) def.members = members;
    const included: string[] = [];
    for (let j = 0; j < i; j++) {
      if (rng.chance(0.3)) included.push('g' + j);
    }
    if (included.length > 0) def.groups = included;
    groups[gid] = def;
  }
  return groups;
}

function randomRules(rng: Rng, groupIds: string[], count: number): Rule[] {
  const rules: Rule[] = [];
  for (let i = 0; i < count; i++) {
    const toGroup = groupIds.length > 0 && rng.chance(0.6);
    rules.push({
      id: 'rule-' + i,
      subject: toGroup ? rng.pick(groupIds) : rng.pick(USERS),
      subjectKind: toGroup ? 'group' : 'user',
      prefix: randomPrefix(rng),
      effect: rng.chance(0.5) ? 'allow' : 'deny',
    });
  }
  return rules;
}

function randomPath(rng: Rng): string {
  const depth = 1 + rng.int(4);
  let p = '';
  for (let i = 0; i < depth; i++) p += '/' + rng.pick(SEGMENTS);
  return p;
}

function decisionShape(d: Decision): unknown {
  return {
    effect: d.effect,
    defaultDeny: d.defaultDeny,
    effective: d.effective ? d.effective.id : null,
    shadowed: d.shadowed.map((r) => r.id),
    matched: d.matched.map((r) => r.id),
  };
}

function compareAllQueries(
  engine: PolicyEngine,
  reference: ReferenceModel,
  rng: Rng,
  queryCount: number,
  label: string,
): void {
  for (let q = 0; q < queryCount; q++) {
    const user = rng.pick(USERS);
    const path = randomPath(rng);
    const a = engine.decide(user, path);
    const b = reference.decide(user, path);
    assertEqual(
      decisionShape(a),
      decisionShape(b),
      `${label}: decision mismatch for (${user}, ${path})`,
    );
  }
}

test('cross-check: engine matches expand-all-groups reference model', () => {
  const rng = makeRng(20261007);
  const engine = new PolicyEngine();
  const reference = new ReferenceModel();

  for (let round = 0; round < 60; round++) {
    const groups = randomGroups(rng, 1 + rng.int(5));
    const rules = randomRules(rng, Object.keys(groups), rng.int(12));
    const config: PolicyConfig = { groups, rules };
    assert(engine.update(config).ok === true, `round ${round}: engine update`);
    assert(reference.update(config).ok === true, `round ${round}: reference update`);
    compareAllQueries(engine, reference, rng, 80, `round ${round}`);
  }
});

test('cross-check: revocation via update sequences', () => {
  const rng = makeRng(770031);
  const engine = new PolicyEngine();
  const reference = new ReferenceModel();

  const groups = randomGroups(rng, 4);
  let rules = randomRules(rng, Object.keys(groups), 10);

  // Apply updates that progressively revoke rules, then re-add some.
  for (let step = 0; step < 12; step++) {
    const config: PolicyConfig = { groups, rules };
    assert(engine.update(config).ok === true, `step ${step}: engine update`);
    assert(reference.update(config).ok === true, `step ${step}: reference update`);
    compareAllQueries(engine, reference, rng, 60, `revoke step ${step}`);

    if (rules.length > 0 && rng.chance(0.7)) {
      // revoke a random rule
      rules = rules.filter((_, i) => i !== rng.int(rules.length));
    } else {
      // grant a new rule (fresh id to keep ids unique)
      rules = rules.concat(
        randomRules(rng, Object.keys(groups), 1).map((r) => ({
          ...r,
          id: 'rule-extra-' + step,
        })),
      );
    }
  }
});

test('cross-check: same-level conflicts resolve to deny', () => {
  const rng = makeRng(4451);
  const engine = new PolicyEngine();
  const reference = new ReferenceModel();

  // Small prefix pool with both effects at identical prefixes forces
  // same-depth allow/deny conflicts across users and groups.
  for (let round = 0; round < 40; round++) {
    const groups = randomGroups(rng, 3);
    const groupIds = Object.keys(groups);
    const prefixes = ['/a', '/a/b', '/'];
    const rules: Rule[] = [];
    for (let i = 0; i < 8; i++) {
      const toGroup = groupIds.length > 0 && rng.chance(0.5);
      rules.push({
        id: 'c' + round + '-' + i,
        subject: toGroup ? rng.pick(groupIds) : rng.pick(USERS),
        subjectKind: toGroup ? 'group' : 'user',
        prefix: rng.pick(prefixes),
        effect: rng.chance(0.5) ? 'allow' : 'deny',
      });
    }
    const config: PolicyConfig = { groups, rules };
    assert(engine.update(config).ok === true, `round ${round}: engine update`);
    assert(reference.update(config).ok === true, `round ${round}: reference update`);
    compareAllQueries(engine, reference, rng, 50, `conflict round ${round}`);

    // Deterministic invariant: whenever a same-depth deny matched,
    // the outcome must be deny regardless of allows at that depth.
    for (const user of USERS) {
      const decision = engine.decide(user, '/a/b/c');
      const sameDepthDeny = decision.matched.some(
        (r) =>
          r.effect === 'deny' &&
          decision.effective !== null &&
          prefixDepth(r.prefix) === prefixDepth(decision.effective.prefix),
      );
      if (sameDepthDeny) {
        assertEqual(decision.effect, 'deny', `deny must win ties for ${user}`);
      }
    }
  }
});

test('cross-check: cyclic configs rejected by both, state preserved', () => {
  const engine = new PolicyEngine();
  const reference = new ReferenceModel();
  const good: PolicyConfig = {
    groups: { a: { members: ['alice'] }, b: { groups: ['a'] } },
    rules: [{ id: 'r1', subject: 'b', subjectKind: 'group', prefix: '/x', effect: 'allow' }],
  };
  assert(engine.update(good).ok === true, 'engine accepts base');
  assert(reference.update(good).ok === true, 'reference accepts base');

  const cyclic: PolicyConfig = {
    groups: { a: { groups: ['b'] }, b: { groups: ['a'] } },
    rules: [{ id: 'r2', subject: 'b', subjectKind: 'group', prefix: '/x', effect: 'deny' }],
  };
  const er = engine.update(cyclic);
  const rr = reference.update(cyclic);
  assert(er.ok === false && rr.ok === false, 'both reject the cycle');
  if (!er.ok && !rr.ok) {
    assertEqual(er.errors.map((e) => e.kind), rr.errors.map((e) => e.kind), 'error kinds');
  }
  // State preserved after the rejected update.
  const rng = makeRng(99);
  compareAllQueries(engine, reference, rng, 40, 'post-rejection');
  assertEqual(engine.decide('alice', '/x').effect, 'allow', 'old config intact');
});
