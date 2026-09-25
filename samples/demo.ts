import { ObligationExecutor } from '../src/executor.ts';
import { InMemoryCompensationLedger, type Obligation } from '../src/obligations.ts';
import type { Policy } from '../src/policy.ts';

const policy: Policy = {
  name: 'refund-policy',
  requiredAttributes: ['role', 'amount', 'riskScore'],
  condition: {
    op: 'and',
    all: [
      { op: 'in', attr: 'role', set: ['admin', 'finance'] },
      { op: 'lte', attr: 'amount', value: 5000 },
      {
        op: 'or',
        any: [
          { op: 'lt', attr: 'riskScore', value: 50 },
          { op: 'not', cond: { op: 'in', attr: 'role', set: ['finance'] } },
        ],
      },
    ],
  },
};

const log: string[] = [];
const obligations: Obligation[] = [
  {
    kind: 'rollbackable',
    id: 'reserve-funds',
    execute: () => void log.push('execute:reserve-funds'),
    compensate: () => void log.push('compensate:reserve-funds'),
  },
  {
    kind: 'rollbackable',
    id: 'update-ledger',
    dependsOn: ['reserve-funds'],
    execute: () => void log.push('execute:update-ledger'),
    compensate: () => void log.push('compensate:update-ledger'),
  },
  {
    kind: 'commit',
    id: 'send-notification',
    execute: () => void log.push('commit:send-notification'),
  },
];

function show(title: string, value: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(value, null, 2));
}

// 场景 1：策略允许，全部义务按依赖顺序执行，提交阶段后返回最终成功。
{
  log.length = 0;
  const executor = new ObligationExecutor(policy, obligations);
  const result = await executor.run({ role: 'admin', amount: 1200, riskScore: 10 });
  show('scenario 1: allow + commit', { result, log });
}

// 场景 2：策略拒绝，任何义务都不执行，绝不返回成功。
{
  log.length = 0;
  const executor = new ObligationExecutor(policy, obligations);
  const result = await executor.run({ role: 'finance', amount: 1200, riskScore: 80 });
  show('scenario 2: deny', { result, log });
}

// 场景 3：第二义务失败，逆序补偿第一义务。
{
  log.length = 0;
  const failing: Obligation[] = [
    obligations[0],
    {
      kind: 'rollbackable',
      id: 'update-ledger',
      dependsOn: ['reserve-funds'],
      execute: () => {
        log.push('execute:update-ledger');
        throw new Error('ledger unavailable');
      },
      compensate: () => void log.push('compensate:update-ledger'),
    },
    obligations[2],
  ];
  const executor = new ObligationExecutor(policy, failing);
  const result = await executor.run({ role: 'admin', amount: 1200, riskScore: 10 });
  show('scenario 3: rollback on second failure', { result, log });
}

// 场景 4：补偿持续失败 -> 保留待处理状态；恢复后幂等重试完成。
{
  log.length = 0;
  const ledger = new InMemoryCompensationLedger();
  let broken = true;
  let effects = 0;
  const flaky: Obligation[] = [
    {
      kind: 'rollbackable',
      id: 'reserve-funds',
      execute: () => void log.push('execute:reserve-funds'),
      compensate: () => {
        if (broken) throw new Error('gateway down');
        effects++;
        log.push('compensate:reserve-funds');
      },
    },
    {
      kind: 'rollbackable',
      id: 'trigger',
      dependsOn: ['reserve-funds'],
      execute: () => {
        throw new Error('trigger rollback');
      },
      compensate: () => {},
    },
  ];
  const executor = new ObligationExecutor(policy, flaky, { maxCompensationAttempts: 2, ledger });
  const first = await executor.run({ role: 'admin', amount: 1200, riskScore: 10 });
  show('scenario 4a: compensation failed, pending retained', { result: first, pending: ledger.pending });
  broken = false;
  const resumed = await executor.resumePendingCompensation({ role: 'admin', amount: 1200, riskScore: 10 });
  show('scenario 4b: resumed, idempotent effect applied once', {
    result: resumed,
    pending: ledger.pending,
    effects,
    log,
  });
}
