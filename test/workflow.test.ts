import test from 'node:test';
import assert from 'node:assert/strict';
import {
  all,
  branch,
  createWorkflow,
  option,
  task,
  type NodeDef,
  type WorkflowEvent,
} from '../src/workflow.ts';

interface Ctx {
  useX: boolean;
  useY: boolean;
  useQ: boolean;
}

function buildDef(): NodeDef<Ctx> {
  return all<Ctx>('root', [
    task<Ctx>('setup'),
    branch<Ctx>('b1', [
      option<Ctx>('opt-x', (c) => c.useX, task<Ctx>('x')),
      option<Ctx>(
        'opt-y',
        (c) => c.useY,
        branch<Ctx>('b2', [
          option<Ctx>('opt-p', () => true, task<Ctx>('p')),
          option<Ctx>(
            'opt-q',
            (c) => c.useQ,
            all<Ctx>('q-all', [task<Ctx>('q1'), task<Ctx>('q2')]),
          ),
        ]),
      ),
      option<Ctx>('opt-z', () => false, task<Ctx>('z')),
    ]),
  ]);
}

function makeWorkflow(ctx: Ctx) {
  const wf = createWorkflow(buildDef(), ctx);
  wf.start();
  return wf;
}

function completeAll(wf: ReturnType<typeof makeWorkflow>, taskIds: string[]) {
  for (const id of taskIds) wf.dispatch({ type: 'complete', taskId: id });
}

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items.slice()];
  const out: T[][] = [];
  for (let i = 0; i < items.length; i++) {
    const rest = items.slice(0, i).concat(items.slice(i + 1));
    for (const tail of permutations(rest)) out.push([items[i], ...tail]);
  }
  return out;
}

test('branch starts only subtasks whose condition holds', () => {
  const wf = makeWorkflow({ useX: true, useY: false, useQ: false });
  assert.equal(wf.statusOf('x'), 'active');
  assert.equal(wf.statusOf('z'), 'skipped');
  assert.equal(wf.statusOf('b2'), 'skipped');
  assert.equal(wf.statusOf('p'), 'skipped');
  assert.deepEqual([...wf.frozenSetOf('b1')], ['opt-x']);
});

test('join waits only for the actually started set, never for unselected branches', () => {
  const wf = makeWorkflow({ useX: true, useY: true, useQ: false });
  assert.equal(wf.statusOf('q-all'), 'skipped');
  assert.equal(wf.statusOf('z'), 'skipped');
  completeAll(wf, ['setup', 'x', 'p']);
  assert.equal(wf.statusOf('b2'), 'completed');
  assert.equal(wf.statusOf('b1'), 'completed');
  assert.equal(wf.isDone(), true);
  const joinB2 = wf.trace.find((e) => e.type === 'join' && e.nodeId === 'b2');
  assert.deepEqual(joinB2 && joinB2.type === 'join' ? joinB2.waited : null, ['p']);
});

test('selected set is frozen at branch selection time', () => {
  const ctx: Ctx = { useX: true, useY: true, useQ: false };
  const wf = makeWorkflow(ctx);
  assert.deepEqual([...wf.frozenSetOf('b2')], ['opt-p']);
  ctx.useQ = true;
  ctx.useX = false;
  assert.deepEqual([...wf.frozenSetOf('b2')], ['opt-p']);
  assert.deepEqual([...wf.frozenSetOf('b1')], ['opt-x', 'opt-y']);
  wf.dispatch({ type: 'complete', taskId: 'q1' });
  assert.deepEqual(wf.evidence, [{ taskId: 'q1', reason: 'not-active' }]);
  completeAll(wf, ['setup', 'x', 'p']);
  assert.equal(wf.isDone(), true);
});

test('duplicate completion events are ignored', () => {
  const wf = makeWorkflow({ useX: true, useY: false, useQ: false });
  completeAll(wf, ['setup', 'x', 'x', 'x']);
  const completedX = wf.trace.filter((e) => e.type === 'completed' && e.nodeId === 'x');
  const duplicates = wf.trace.filter((e) => e.type === 'duplicate-complete' && e.taskId === 'x');
  assert.equal(completedX.length, 1);
  assert.equal(duplicates.length, 2);
  assert.equal(wf.joinCount('b1'), 1);
  assert.equal(wf.isDone(), true);
});

test('join fires exactly once under every event permutation', () => {
  const tasks = ['setup', 'x', 'p', 'q1', 'q2'];
  const orders = permutations(tasks);
  assert.equal(orders.length, 120);
  for (const order of orders) {
    const wf = makeWorkflow({ useX: true, useY: true, useQ: true });
    completeAll(wf, order);
    assert.equal(wf.joinCount('b1'), 1, `b1 join count for ${order}`);
    assert.equal(wf.joinCount('b2'), 1, `b2 join count for ${order}`);
    assert.equal(wf.joinCount('q-all'), 1, `q-all join count for ${order}`);
    assert.equal(wf.joinCount('root'), 1, `root join count for ${order}`);
    assert.equal(wf.isDone(), true, `done for ${order}`);
    for (const id of tasks) assert.equal(wf.statusOf(id), 'completed');
  }
});

test('join fires exactly once under permutations with duplicate events', () => {
  const tasks = ['setup', 'x', 'p', 'x'];
  for (const order of permutations(tasks)) {
    const wf = makeWorkflow({ useX: true, useY: true, useQ: false });
    completeAll(wf, order);
    assert.equal(wf.joinCount('b1'), 1, `b1 join count for ${order}`);
    assert.equal(wf.joinCount('b2'), 1, `b2 join count for ${order}`);
    assert.equal(wf.joinCount('root'), 1, `root join count for ${order}`);
    assert.equal(wf.isDone(), true, `done for ${order}`);
  }
});

test('nested branch joins inner-first and outer waits for inner join only', () => {
  const wf = makeWorkflow({ useX: true, useY: true, useQ: true });
  completeAll(wf, ['setup', 'x', 'p', 'q1', 'q2']);
  const joins = wf.trace.filter((e) => e.type === 'join').map((e) => (e.type === 'join' ? e.nodeId : ''));
  assert.ok(joins.indexOf('q-all') < joins.indexOf('b2'), 'inner all joins before inner branch');
  assert.ok(joins.indexOf('b2') < joins.indexOf('b1'), 'inner branch joins before outer branch');
  assert.ok(joins.indexOf('b1') < joins.indexOf('root'), 'outer branch joins before root');
  assert.equal(wf.isDone(), true);
});

test('late success after cancel records evidence only and never advances flow', () => {
  const wf = makeWorkflow({ useX: true, useY: true, useQ: true });
  wf.dispatch({ type: 'cancel', nodeId: 'b2' });
  assert.equal(wf.statusOf('b2'), 'cancelled');
  assert.equal(wf.statusOf('p'), 'cancelled');
  assert.equal(wf.statusOf('q1'), 'cancelled');
  completeAll(wf, ['p', 'q1', 'q2']);
  assert.deepEqual(wf.evidence, [
    { taskId: 'p', reason: 'late-success-after-cancel' },
    { taskId: 'q1', reason: 'late-success-after-cancel' },
    { taskId: 'q2', reason: 'late-success-after-cancel' },
  ]);
  assert.equal(wf.statusOf('p'), 'cancelled');
  assert.equal(wf.joinCount('b2'), 0);
  completeAll(wf, ['setup', 'x']);
  assert.equal(wf.joinCount('b1'), 0);
  assert.equal(wf.isDone(), false);
});

test('cancelling a single task keeps its late success as evidence and blocks its join', () => {
  const wf = makeWorkflow({ useX: true, useY: true, useQ: true });
  wf.dispatch({ type: 'cancel', nodeId: 'q2' });
  completeAll(wf, ['setup', 'x', 'p', 'q1', 'q2']);
  assert.deepEqual(wf.evidence, [{ taskId: 'q2', reason: 'late-success-after-cancel' }]);
  assert.equal(wf.joinCount('q-all'), 0);
  assert.equal(wf.joinCount('b2'), 0);
  assert.equal(wf.isDone(), false);
});

test('empty selection joins immediately', () => {
  const wf = makeWorkflow({ useX: false, useY: false, useQ: false });
  assert.equal(wf.joinCount('b1'), 1);
  assert.equal(wf.statusOf('b1'), 'completed');
  completeAll(wf, ['setup']);
  assert.equal(wf.isDone(), true);
});

test('completion of skipped or unknown tasks is evidence, not progress', () => {
  const wf = makeWorkflow({ useX: true, useY: false, useQ: false });
  wf.dispatch({ type: 'complete', taskId: 'z' });
  wf.dispatch({ type: 'complete', taskId: 'ghost' });
  assert.deepEqual(wf.evidence, [
    { taskId: 'z', reason: 'not-active' },
    { taskId: 'ghost', reason: 'unknown-task' },
  ]);
  assert.equal(wf.joinCount('b1'), 0);
});

test('cancelling a completed node is ignored', () => {
  const wf = makeWorkflow({ useX: true, useY: false, useQ: false });
  completeAll(wf, ['setup', 'x']);
  assert.equal(wf.isDone(), true);
  wf.dispatch({ type: 'cancel', nodeId: 'root' });
  assert.equal(wf.statusOf('root'), 'completed');
  assert.ok(wf.trace.some((e) => e.type === 'ignored-cancel' && e.nodeId === 'root'));
});
