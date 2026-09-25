import { solve, verify, type PolicyExpr, type Problem } from '../src/index.ts';

const hasSkill = (skill: string): PolicyExpr => ({ op: 'in', attr: 'skills', set: [skill] });
const senior = (level: number): PolicyExpr => ({ op: 'gte', attr: 'level', value: level });

const positions = [
  { id: 'initiator', requires: hasSkill('payment-init') },
  { id: 'reviewer', requires: { op: 'and', args: [hasSkill('payment-review'), senior(4)] } },
  { id: 'approver', requires: { op: 'and', args: [hasSkill('payment-approve'), senior(6)] } },
];

const subjects = [
  { id: 'alice', attributes: { dept: 'finance', level: 5, skills: ['payment-init', 'payment-review'] } },
  { id: 'bob', attributes: { dept: 'finance', level: 4, skills: ['payment-review'] } },
  { id: 'carol', attributes: { dept: 'ops', level: 7, skills: ['payment-approve', 'payment-review'] } },
  { id: 'dave', aliases: ['d.wu'], attributes: { dept: 'finance', level: 3, skills: ['payment-init'] } },
];

const costs = {
  initiator: { alice: 3, dave: 1 },
  reviewer: { alice: 4, bob: 2, carol: 3 },
  approver: { carol: 1 },
};

const show = (label: string, value: unknown) => {
  console.log(`\n=== ${label} ===`);
  console.log(JSON.stringify(value, null, 2));
};

// 1. Baseline: cheapest fully separated staffing.
const baseline: Problem = { positions, subjects, costs };
const plan = solve(baseline);
show('1. baseline solve (cost optimised)', plan);
if (plan.status === 'feasible') {
  show('1b. independent verifier', verify(baseline, plan.assignment));
}

// 2. Same person initiates and reviews: rejected.
show(
  '2. initiator == reviewer, no exception',
  verify(baseline, { initiator: 'alice', reviewer: 'alice', approver: 'carol' }),
);

// 3. Aliases cannot bypass: "d.wu" is dave.
show(
  '3. alias bypass attempt (initiator dave, reviewer d.wu)',
  verify(baseline, { initiator: 'dave', reviewer: 'd.wu', approver: 'carol' }),
);

// 4. Emergency: only alice can initiate and review. An explicit exception
//    within budget 1 makes the plan issuable.
const skeletonCrew = [subjects[0]!, subjects[2]!]; // alice, carol
const emergency: Problem = {
  positions,
  subjects: skeletonCrew,
  exceptions: [{ id: 'EX-7', positions: ['initiator', 'reviewer'], subject: 'alice' }],
  exceptionBudget: 1,
};
const emergencyPlan = solve(emergency);
show('4. emergency: explicit exception within budget 1', emergencyPlan);

// 5. Same exception on file but budget 0: only pending, never a pass.
show('5. same exception, budget 0', solve({ ...emergency, exceptionBudget: 0 }));

// 6. Staff returns and the exception is withdrawn: repair the old plan with
//    minimal personnel changes, then minimal cost.
if (emergencyPlan.status === 'feasible') {
  const repaired = solve({ positions, subjects, costs, existingAssignment: emergencyPlan.assignment });
  show('6. repair after withdrawing the exception', repaired);
}

// 7. Hall deficiency: three roles, two qualified people.
const hall = solve({
  positions: [
    { id: 'r1', requires: hasSkill('audit') },
    { id: 'r2', requires: hasSkill('audit') },
    { id: 'r3', requires: hasSkill('audit') },
  ],
  subjects: [
    { id: 's1', attributes: { skills: ['audit'] } },
    { id: 's2', attributes: { skills: ['audit'] } },
  ],
});
show('7. Hall deficiency (3 audit roles, 2 auditors)', hall);

// 8. Partial check: unassigned positions are pending, not a pass.
show('8. partial assignment check', verify(baseline, { initiator: 'dave' }));
