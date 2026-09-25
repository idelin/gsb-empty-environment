import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  evaluatePolicy,
  PolicyError,
  solve,
  verify,
  type PolicyExpr,
  type Problem,
} from '../src/index.ts';

const skilled = (id: string, skills: string[], extra: Record<string, number | string | boolean> = {}) => ({
  id,
  attributes: { skills, ...extra },
});

const skillPosition = (id: string, skill: string) => ({
  id,
  requires: { op: 'in', attr: 'skills', set: [skill] } as PolicyExpr,
});

test('initiator and reviewer held by the same subject without exception is rejected', () => {
  const problem: Problem = {
    positions: [skillPosition('initiator', 'initiate'), skillPosition('reviewer', 'review')],
    subjects: [skilled('alice', ['initiate', 'review']), skilled('bob', ['review'])],
  };
  const result = verify(problem, { initiator: 'alice', reviewer: 'alice' });
  assert.equal(result.status, 'invalid');
  assert.match(result.violations.join('\n'), /both held by subject "alice"/);

  // Only one qualified subject for both roles: no complete plan exists.
  const alone = solve({
    positions: problem.positions,
    subjects: [skilled('alice', ['initiate', 'review'])],
  });
  assert.equal(alone.status, 'infeasible');
  assert.deepEqual(alone.conflictPositions.length, 1);
});

test('aliases resolve to the same subject and cannot bypass separation', () => {
  const problem: Problem = {
    positions: [skillPosition('initiator', 'initiate'), skillPosition('reviewer', 'review')],
    subjects: [
      { id: 'alice', aliases: ['a.chen', 'chen.a'], attributes: { skills: ['initiate', 'review'] } },
    ],
  };
  const result = verify(problem, { initiator: 'alice', reviewer: 'a.chen' });
  assert.equal(result.status, 'invalid');
  assert.match(result.violations.join('\n'), /both held by subject "alice"/);
});

test('two subjects claiming the same alias are rejected outright', () => {
  const problem: Problem = {
    positions: [skillPosition('p1', 'x')],
    subjects: [
      { id: 'alice', aliases: ['shared'], attributes: { skills: ['x'] } },
      { id: 'bob', aliases: ['shared'], attributes: { skills: ['x'] } },
    ],
  };
  assert.throws(() => solve(problem), /claimed by both/);
  assert.throws(() => verify(problem, { p1: 'shared' }), /maps to both/);
});

test('Hall deficiency is reported when qualified subjects are too few', () => {
  const problem: Problem = {
    positions: [skillPosition('p1', 'x'), skillPosition('p2', 'x'), skillPosition('p3', 'x')],
    subjects: [skilled('s1', ['x']), skilled('s2', ['x'])],
  };
  const result = solve(problem);
  assert.equal(result.status, 'infeasible');
  assert.equal(result.conflictPositions.length, 1, 'dropping any one position restores feasibility');
  assert.ok(result.hallDeficiency);
  assert.equal(result.hallDeficiency.positions.length, 3);
  assert.deepEqual(result.hallDeficiency.subjects, ['s1', 's2']);
});

test('alias merge creates a new conflict and replan repairs it with minimal changes', () => {
  const positions = [skillPosition('initiator', 'initiate'), skillPosition('reviewer', 'review')];
  const before: Problem = {
    positions,
    subjects: [skilled('alice', ['initiate']), skilled('bob', ['review'])],
  };
  const existing = { initiator: 'alice', reviewer: 'bob' };
  assert.equal(verify(before, existing).status, 'valid');

  // "bob" is merged into alice as an alias: the old assignment now conflicts.
  const afterSubjects = [
    { id: 'alice', aliases: ['bob'], attributes: { skills: ['initiate', 'review'] } },
    skilled('carol', ['review']),
  ];
  const after: Problem = { positions, subjects: afterSubjects };
  const recheck = verify(after, existing);
  assert.equal(recheck.status, 'invalid');
  assert.match(recheck.violations.join('\n'), /both held by subject "alice"/);

  const repaired = solve({ ...after, existingAssignment: existing });
  assert.equal(repaired.status, 'feasible');
  assert.equal(repaired.changes, 1, 'only the conflicted position is re-staffed');
  assert.equal(repaired.assignment.initiator, 'alice');
  assert.equal(repaired.assignment.reviewer, 'carol');
  assert.equal(verify(after, repaired.assignment).status, 'valid');
});

test('explicit exception allows shared subject within budget; budget exhaustion is pending', () => {
  const exception = { id: 'E1', positions: ['initiator', 'reviewer'], subject: 'alice' };
  const base: Problem = {
    positions: [skillPosition('initiator', 'initiate'), skillPosition('reviewer', 'review')],
    subjects: [skilled('alice', ['initiate', 'review'])],
    exceptions: [exception],
  };

  const withinBudget = solve({ ...base, exceptionBudget: 1 });
  assert.equal(withinBudget.status, 'feasible');
  assert.deepEqual(withinBudget.exceptionsUsed, ['E1']);
  assert.equal(
    verify({ ...base, exceptionBudget: 1 }, withinBudget.assignment).status,
    'valid',
  );

  const noBudget = solve(base); // budget defaults to 0
  assert.equal(noBudget.status, 'pending');
  assert.deepEqual(noBudget.requiredExceptions, ['E1']);

  const verifiedOverBudget = verify(base, { initiator: 'alice', reviewer: 'alice' });
  assert.equal(verifiedOverBudget.status, 'invalid');
  assert.match(verifiedOverBudget.violations.join('\n'), /budget exceeded/);
});

test('withdrawing an exception triggers minimal-change, cost-optimised repair', () => {
  const positions = [
    skillPosition('initiator', 'initiate'),
    skillPosition('reviewer', 'review'),
    skillPosition('approver', 'approve'),
  ];
  const subjects = [
    skilled('alice', ['initiate', 'review']),
    skilled('bob', ['review']),
    skilled('carol', ['review']),
    skilled('dave', ['approve']),
  ];
  const legacy = { initiator: 'alice', reviewer: 'alice', approver: 'dave' };
  const withException: Problem = {
    positions,
    subjects,
    exceptions: [{ id: 'E1', positions: ['initiator', 'reviewer'], subject: 'alice' }],
    exceptionBudget: 1,
  };
  assert.equal(verify(withException, legacy).status, 'valid');

  // Exception withdrawn: exactly one position must change hands, and among
  // the qualified alternates the cheapest one wins.
  const costs = { reviewer: { bob: 5, carol: 2 } };
  const repaired = solve({ positions, subjects, costs, existingAssignment: legacy });
  assert.equal(repaired.status, 'feasible');
  assert.equal(repaired.changes, 1, 'only the conflicted position is re-staffed');
  assert.equal(repaired.assignment.initiator, 'alice');
  assert.equal(repaired.assignment.approver, 'dave');
  assert.equal(repaired.assignment.reviewer, 'carol', 'cheapest qualified alternate');
  assert.equal(repaired.cost, 2);
  assert.equal(verify({ positions, subjects }, repaired.assignment).status, 'valid');
});

test('personnel changes are minimised before cost, cost breaks ties', () => {
  const reviewerPolicy: PolicyExpr = {
    op: 'and',
    args: [
      { op: 'in', attr: 'skills', set: ['review'] },
      { op: 'gte', attr: 'level', value: 5 },
    ],
  };
  const positions = [skillPosition('initiator', 'initiate'), { id: 'reviewer', requires: reviewerPolicy }];
  const subjects = [
    skilled('alice', ['initiate']),
    skilled('carol', ['initiate', 'review'], { level: 9 }),
    skilled('dave', ['review'], { level: 9 }),
  ];
  // Keeping alice costs 100; restaffing both positions would cost only 2.
  const costs = {
    initiator: { alice: 100, carol: 1 },
    reviewer: { carol: 1, dave: 1 },
  };
  const existing = { initiator: 'alice', reviewer: 'carol' };
  const repaired = solve({ positions, subjects, costs, existingAssignment: existing });
  assert.equal(repaired.status, 'feasible');
  assert.equal(repaired.changes, 0, 'the current plan is still feasible, so nobody moves');
  assert.equal(repaired.cost, 101);

  // Force a repair: carol leaves (no longer listed). One change at cost 101
  // must still beat two changes at cost 2.
  const withoutCarol = [subjects[0]!, subjects[2]!];
  const forced = solve({
    positions,
    subjects: withoutCarol,
    costs,
    existingAssignment: existing,
  });
  assert.equal(forced.status, 'feasible');
  assert.equal(forced.changes, 1, 'fewest personnel changes wins over lower cost');
  assert.equal(forced.assignment.initiator, 'alice');
  assert.equal(forced.assignment.reviewer, 'dave');

  // Without a baseline, cost alone drives the choice.
  const fresh = solve({ positions, subjects, costs });
  assert.equal(fresh.status, 'feasible');
  assert.equal(fresh.assignment.initiator, 'carol');
  assert.equal(fresh.assignment.reviewer, 'dave');
  assert.equal(fresh.cost, 2);
});

test('partial assignments: satisfiable extension and hard infeasibility', () => {
  const positions = [skillPosition('p1', 'x'), skillPosition('p2', 'y')];
  const subjects = [skilled('alice', ['x']), skilled('bob', ['y']), skilled('carol', ['x', 'y'])];
  const extended = solve({ positions, subjects, fixed: { p1: 'alice' } });
  assert.equal(extended.status, 'feasible');
  assert.equal(extended.assignment.p1, 'alice');
  assert.notEqual(extended.assignment.p2, 'alice', 'separation is preserved');

  const stuck = solve({ positions, subjects, fixed: { p1: 'bob' } });
  assert.equal(stuck.status, 'infeasible');
  assert.deepEqual(stuck.conflictPositions, ['p1'], 'bob is not qualified for p1');

  // Fixed assignment that can only be extended by double-staffing carol.
  const shortStaffed = solve({
    positions,
    subjects: [skilled('carol', ['x', 'y'])],
    fixed: { p2: 'carol' },
  });
  assert.equal(shortStaffed.status, 'infeasible');
});

test('unassigned positions yield pending, never a pass', () => {
  const problem: Problem = {
    positions: [skillPosition('initiator', 'initiate'), skillPosition('reviewer', 'review')],
    subjects: [skilled('alice', ['initiate']), skilled('bob', ['review'])],
  };
  const result = verify(problem, { initiator: 'alice' });
  assert.equal(result.status, 'pending');
  assert.deepEqual(result.unassigned, ['reviewer']);
});

test('policy language: finite sets, integer conditions, boolean combinations', () => {
  const policy: PolicyExpr = {
    op: 'and',
    args: [
      { op: 'gte', attr: 'level', value: 3 },
      { op: 'in', attr: 'dept', set: ['finance', 'ops'] },
      {
        op: 'or',
        args: [
          { op: 'has', attr: 'certified' },
          { op: 'eq', attr: 'clearance', value: 'high' },
        ],
      },
      { op: 'not', arg: { op: 'eq', attr: 'status', value: 'suspended' } },
    ],
  };

  assert.equal(
    evaluatePolicy(policy, { level: 3, dept: 'finance', certified: true }),
    true,
  );
  assert.equal(
    evaluatePolicy(policy, { level: 4, dept: 'ops', clearance: 'high' }),
    true,
  );
  assert.equal(
    evaluatePolicy(policy, { level: 2, dept: 'finance', certified: true }),
    false,
    'integer condition fails',
  );
  assert.equal(
    evaluatePolicy(policy, { level: 5, dept: 'eng', certified: true }),
    false,
    'finite-set membership fails',
  );
  assert.equal(
    evaluatePolicy(policy, { level: 5, dept: 'ops', certified: true, status: 'suspended' }),
    false,
    'negated condition fails',
  );
  assert.equal(evaluatePolicy(policy, {}), false, 'missing attributes are closed-world false');
  assert.equal(
    evaluatePolicy({ op: 'gte', attr: 'level', value: 3 }, { level: 2.5 }),
    false,
    'non-integer attribute never satisfies an integer condition',
  );
  assert.throws(
    () => evaluatePolicy({ op: 'gte', attr: 'level', value: 2.5 }, { level: 3 }),
    PolicyError,
    'non-integer policy value is a modelling error',
  );
});

test('solver output always passes the independent verifier (seeded random agreement)', () => {
  // Deterministic PRNG (mulberry32).
  let state = 0x2f6e2b1;
  const rand = () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = <T,>(items: readonly T[]): T => items[Math.floor(rand() * items.length)] as T;

  const skills = ['a', 'b'];
  for (let round = 0; round < 200; round++) {
    const positionCount = 2 + Math.floor(rand() * 3);
    const subjectCount = 1 + Math.floor(rand() * 4);
    const positions = Array.from({ length: positionCount }, (_, i) =>
      skillPosition(`p${i}`, pick(skills)),
    );
    const subjects = Array.from({ length: subjectCount }, (_, i) =>
      skilled(`s${i}`, skills.filter(() => rand() < 0.6)),
    );
    const problem: Problem = { positions, subjects };
    const result = solve(problem);
    if (result.status === 'feasible') {
      const check = verify(problem, result.assignment);
      assert.equal(check.status, 'valid', `round ${round}: verifier must confirm solver output`);
      const holders = Object.values(result.assignment);
      assert.equal(new Set(holders).size, holders.length, 'canonical subjects are distinct');
    } else {
      assert.equal(result.status, 'infeasible', 'no exceptions exist, so pending is impossible');
      assert.ok(result.conflictPositions.length >= 1);
      const remaining = positions.filter((p) => !result.conflictPositions.includes(p.id));
      const repaired = solve({ positions: remaining, subjects });
      assert.equal(
        repaired.status,
        'feasible',
        `round ${round}: removing the conflict set restores feasibility`,
      );
      if (result.hallDeficiency) {
        assert.ok(
          result.hallDeficiency.positions.length > result.hallDeficiency.subjects.length,
          'Hall witness: more positions than qualified subjects',
        );
      }
    }
  }
});
