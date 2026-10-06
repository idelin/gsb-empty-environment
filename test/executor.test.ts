import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { EventStore } from '../src/eventStore.ts';
import type { StepEvent } from '../src/events.ts';
import { SagaExecutor, type StepDef } from '../src/executor.ts';
import { FaultInjector, SimulatedCrashError } from '../src/faults.ts';

class Ledger {
  readonly attempts: string[] = [];
  readonly applied: string[] = [];
  private readonly done = new Set<string>();

  apply(key: string): void {
    this.attempts.push(key);
    if (!this.done.has(key)) {
      this.done.add(key);
      this.applied.push(key);
    }
  }
}

function ledgerSteps(spec: Array<[string, ...string[]]>): StepDef<Ledger>[] {
  return spec.map(([id, ...deps]) => ({
    id,
    dependsOn: deps,
    run: (ledger) => ledger.apply(`run:${id}`),
    compensate: (ledger) => ledger.apply(`comp:${id}`),
  }));
}

function setup(steps: StepDef<Ledger>[], faults?: FaultInjector) {
  const dir = mkdtempSync(join(tmpdir(), 'saga-'));
  const path = join(dir, 'events.jsonl');
  const ledger = new Ledger();
  const make = () =>
    new SagaExecutor<Ledger>({
      workflowId: 'wf-1',
      steps,
      ctx: ledger,
      store: new EventStore(path, 'wf-1'),
      ...(faults ? { faults } : {}),
    });
  const events = (): StepEvent[] =>
    readFileSync(path, 'utf8')
      .split('\n')
      .filter((l) => l.trim().length > 0)
      .map((l) => JSON.parse(l) as StepEvent);
  return { ledger, make, events };
}

const count = (xs: string[], v: string) => xs.filter((x) => x === v).length;
const ofType = (events: StepEvent[], type: StepEvent['type'], stepId?: string) =>
  events.filter((e) => e.type === type && (stepId === undefined || e.stepId === stepId));

test('回执丢失：崩溃在回执写入前，恢复后幂等步骤 id 保证副作用只应用一次', async () => {
  const faults = new FaultInjector().crashAt('run_receipt', 'B');
  const { ledger, make, events } = setup(ledgerSteps([['A'], ['B', 'A'], ['C', 'B']]), faults);

  await assert.rejects(make().execute(), SimulatedCrashError);
  assert.deepEqual(ledger.applied, ['run:A', 'run:B']);

  const status = await make().recover();
  assert.equal(status, 'completed');
  assert.equal(count(ledger.attempts, 'run:B'), 2, 'B 的 run 被重放');
  assert.equal(count(ledger.applied, 'run:B'), 1, '幂等键去重，副作用只应用一次');
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'run:C']);

  const all = events();
  assert.equal(ofType(all, 'run_started', 'B').length, 2);
  assert.equal(ofType(all, 'step_completed', 'B').length, 1);
  assert.equal(ofType(all, 'workflow_completed').length, 1);
});

test('分支成功：仅补偿已成功且未补偿的步骤，按反向依赖顺序', async () => {
  const faults = new FaultInjector().failAt('run', 'C');
  const steps = ledgerSteps([['A'], ['B', 'A'], ['C', 'A'], ['D', 'B']]);
  const { ledger, make, events } = setup(steps, faults);

  const status = await make().execute();
  assert.equal(status, 'compensated');

  assert.ok(!ledger.attempts.includes('run:D'), '失败后不再调度后续步骤 D');
  assert.ok(!ledger.attempts.includes('comp:C'), '失败的 C 不补偿');
  assert.ok(!ledger.attempts.includes('comp:D'), '未执行的 D 不补偿');
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'comp:B', 'comp:A'], '反向依赖序补偿');

  const all = events();
  assert.equal(ofType(all, 'step_failed', 'C').length, 1);
  assert.equal(ofType(all, 'compensation_started', 'C').length, 0);
  assert.equal(ofType(all, 'compensation_started', 'D').length, 0);
  assert.equal(ofType(all, 'workflow_failed').length, 1);
});

test('补偿失败允许恢复重试，成功补偿不能重复', async () => {
  const faults = new FaultInjector().failAt('run', 'C').failAt('compensate', 'B', 1);
  const { ledger, make, events } = setup(ledgerSteps([['A'], ['B', 'A'], ['C', 'B']]), faults);

  const first = await make().execute();
  assert.equal(first, 'compensation_failed');
  assert.equal(ofType(events(), 'compensation_failed', 'B').length, 1);

  const second = await make().recover();
  assert.equal(second, 'compensated');
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'comp:B', 'comp:A']);
  assert.equal(ofType(events(), 'step_compensated', 'B').length, 1);

  const before = events().length;
  const appliedBefore = ledger.applied.length;
  const third = await make().recover();
  assert.equal(third, 'compensated');
  assert.equal(events().length, before, '终态后 recover 不产生新事件');
  assert.equal(ledger.applied.length, appliedBefore, '成功补偿不重复执行');
});

test('补偿回执丢失：恢复后重试补偿，幂等键保证副作用不重复', async () => {
  const faults = new FaultInjector().failAt('run', 'C').crashAt('compensate_receipt', 'B');
  const { ledger, make, events } = setup(ledgerSteps([['A'], ['B', 'A'], ['C', 'B']]), faults);

  await assert.rejects(make().execute(), SimulatedCrashError);
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'comp:B']);

  const status = await make().recover();
  assert.equal(status, 'compensated');
  assert.equal(count(ledger.attempts, 'comp:B'), 2, '补偿被重试');
  assert.equal(count(ledger.applied, 'comp:B'), 1, '幂等键去重，补偿副作用只应用一次');
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'comp:B', 'comp:A']);

  const all = events();
  assert.equal(ofType(all, 'compensation_started', 'B').length, 2);
  assert.equal(ofType(all, 'step_compensated', 'B').length, 1);
  assert.equal(ofType(all, 'workflow_failed').length, 1);
});

test('幂等步骤 id：重复 id、未知依赖与环均被拒绝', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'saga-'));
  const store = () => new EventStore(join(dir, 'e.jsonl'), 'wf-x');
  const ledger = new Ledger();

  assert.throws(
    () =>
      new SagaExecutor({
        workflowId: 'wf-x',
        steps: [
          { id: 'A', run: () => {} },
          { id: 'A', run: () => {} },
        ],
        ctx: ledger,
        store: store(),
      }),
    /duplicate step id/,
  );
  assert.throws(
    () =>
      new SagaExecutor({
        workflowId: 'wf-x',
        steps: [{ id: 'A', dependsOn: ['ghost'], run: () => {} }],
        ctx: ledger,
        store: store(),
      }),
    /unknown dependency/,
  );
  assert.throws(
    () =>
      new SagaExecutor({
        workflowId: 'wf-x',
        steps: [
          { id: 'A', dependsOn: ['B'], run: () => {} },
          { id: 'B', dependsOn: ['A'], run: () => {} },
        ],
        ctx: ledger,
        store: store(),
      }),
    /cycle/,
  );
});

test('happy path：全部成功后 recover 是幂等空操作', async () => {
  const { ledger, make, events } = setup(ledgerSteps([['A'], ['B', 'A'], ['C', 'A']]));
  assert.equal(await make().execute(), 'completed');
  assert.deepEqual(ledger.applied, ['run:A', 'run:B', 'run:C']);

  const before = events().length;
  assert.equal(await make().recover(), 'completed');
  assert.equal(events().length, before);
  assert.equal(ledger.applied.length, 3);
});
