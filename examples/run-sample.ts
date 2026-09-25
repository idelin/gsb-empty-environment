import { ObligationExecutor, type Obligation } from '../src/executor.ts';
import type { Policy } from '../src/policy.ts';

const purchasePolicy: Policy = {
  id: 'purchase',
  condition: {
    op: 'and',
    all: [
      { op: 'in', attr: 'role', set: ['buyer', 'vip'] },
      { op: 'gte', attr: 'balance', value: 100 },
      { op: 'not', cond: { op: 'in', attr: 'region', set: ['embargoed'] } },
    ],
  },
};

const world = {
  inventory: 10,
  charged: 0,
  emails: 0,
  failCharge: false,
  releaseFlakyFailures: 0,
};

function buildObligations(): Obligation[] {
  return [
    {
      id: 'reserve-inventory',
      kind: 'rollbackable',
      execute: () => {
        world.inventory -= 1;
        console.log('  [execute]    reserve-inventory  inventory=' + world.inventory);
      },
      compensate: () => {
        if (world.releaseFlakyFailures > 0) {
          world.releaseFlakyFailures--;
          throw new Error('inventory service temporarily unavailable');
        }
        world.inventory += 1;
        console.log('  [compensate] release-inventory  inventory=' + world.inventory);
      },
    },
    {
      id: 'charge-payment',
      kind: 'rollbackable',
      dependsOn: ['reserve-inventory'],
      execute: () => {
        if (world.failCharge) throw new Error('card declined');
        world.charged += 100;
        console.log('  [execute]    charge-payment    charged=' + world.charged);
      },
      compensate: () => {
        world.charged -= 100;
        console.log('  [compensate] refund-payment    charged=' + world.charged);
      },
    },
    {
      id: 'send-confirmation',
      kind: 'non-rollbackable',
      execute: () => {
        world.emails += 1;
        console.log('  [commit]     send-confirmation emails=' + world.emails);
      },
    },
  ];
}

function show(label: string, result: unknown) {
  console.log(label);
  console.log(' ', JSON.stringify(result));
  console.log();
}

const executor = new ObligationExecutor({ maxCompensationAttempts: 3, retryDelayMs: 50 });

console.log('--- Scenario 1: policy denies (role not in allowed set) ---');
show('result:', await executor.authorizeAndRun(
  purchasePolicy,
  { role: 'guest', balance: 500, region: 'eu' },
  buildObligations(),
  'run-denied',
));

console.log('--- Scenario 2: allowed, all obligations succeed ---');
show('result:', await executor.authorizeAndRun(
  purchasePolicy,
  { role: 'vip', balance: 250, region: 'eu' },
  buildObligations(),
  'run-success',
));

console.log('--- Scenario 3: second obligation fails, first is compensated ---');
world.failCharge = true;
show('result:', await executor.authorizeAndRun(
  purchasePolicy,
  { role: 'buyer', balance: 200, region: 'us' },
  buildObligations(),
  'run-rollback',
));
world.failCharge = false;

console.log('--- Scenario 4: compensation keeps failing -> pending, then resumed ---');
world.failCharge = true;
world.releaseFlakyFailures = 5;
const obligations = buildObligations();
const pending = await executor.authorizeAndRun(
  purchasePolicy,
  { role: 'buyer', balance: 200, region: 'us' },
  obligations,
  'run-pending',
);
show('result (compensation exhausted retries):', pending);
world.failCharge = false;
world.releaseFlakyFailures = 0;
show('resume result:', await executor.resumePendingCompensations('run-pending', obligations));

console.log('final world state:', JSON.stringify(world));
