import assert from 'node:assert/strict';
import test from 'node:test';
import {
  Store,
  type Command,
  CommandFailedError,
  VersionConflictError,
} from '../src/index.ts';

interface Counter {
  value: number;
  ops: string[];
}

const makeStore = () => new Store<Counter>({ value: 0, ops: [] });

function add(name: string, n: number, expectedVersion?: number): Command<Counter> {
  return {
    name,
    expectedVersion,
    execute(s) {
      s.value += n;
      s.ops.push(`exec:${name}`);
    },
    undo(s) {
      s.value -= n;
      s.ops.push(`undo:${name}`);
    },
  };
}

function boom(name: string): Command<Counter> {
  return {
    name,
    execute() {
      throw new Error('boom');
    },
    undo() {},
  };
}

test('命令执行并提交，版本递增', () => {
  const store = makeStore();
  store.commit([add('a', 5)]);
  assert.equal(store.getState().value, 5);
  assert.equal(store.getVersion(), 1);
});

test('前置版本匹配才允许执行，不匹配则冲突且不产生副作用', () => {
  const store = makeStore();
  store.commit([add('a', 1)]);
  assert.throws(
    () => store.commit([add('b', 1, 0)]),
    (err: unknown) =>
      err instanceof VersionConflictError &&
      err.expectedVersion === 0 &&
      err.actualVersion === 1,
  );
  assert.equal(store.getState().value, 1);
  assert.equal(store.getVersion(), 1);
  store.commit([add('c', 1, 1)]);
  assert.equal(store.getState().value, 2);
});

test('多个命令组合为原子提交：单条历史、单次版本递增', () => {
  const store = makeStore();
  const node = store.commit([add('a', 1), add('b', 2), add('c', 3)]);
  assert.equal(store.getState().value, 6);
  assert.equal(store.getVersion(), 1);
  assert.equal(store.history().length, 1);
  assert.equal(node.commands.length, 3);
});

test('中间命令失败：已执行部分按反向回滚，状态与版本不变', () => {
  const store = makeStore();
  store.commit([add('base', 10)]);
  const undoOrder: string[] = [];
  const spy = (name: string, n: number): Command<Counter> => ({
    name,
    execute(s) {
      s.value += n;
    },
    undo(s) {
      s.value -= n;
      undoOrder.push(name);
    },
  });
  assert.throws(
    () => store.commit([spy('x', 1), spy('y', 2), boom('z')]),
    (err: unknown) =>
      err instanceof CommandFailedError &&
      err.commandName === 'z' &&
      assert.deepEqual(err.rolledBack, ['y', 'x']) === undefined,
  );
  assert.deepEqual(undoOrder, ['y', 'x']);
  assert.equal(store.getState().value, 10);
  assert.equal(store.getVersion(), 1);
  assert.equal(store.history().length, 1);
});

test('撤销顺序反向：逆操作按命令逆序执行', () => {
  const store = makeStore();
  store.commit([add('a', 1), add('b', 2), add('c', 3)]);
  store.undo();
  const ops = store.replay().ops;
  assert.deepEqual(ops, []);
  const committed = store.getState();
  assert.equal(committed.value, 0);
});

test('组合后逆操作完整：undo 组合提交后回到提交前状态', () => {
  const store = makeStore();
  store.commit([add('base', 7)]);
  const before = store.getState().value;
  store.commit([add('a', 1), add('b', 2), add('c', 3)]);
  assert.equal(store.getState().value, before + 6);
  store.undo();
  assert.equal(store.getState().value, before);
  assert.equal(store.getVersion(), 1);
});

test('撤销前状态已改变：抛冲突而非误改，状态保持不动', () => {
  const store = makeStore();
  const stale = store.commit([add('a', 5)]);
  store.commit([add('b', 5)]);
  assert.throws(
    () => store.undoCommit(stale.id),
    (err: unknown) => err instanceof VersionConflictError,
  );
  assert.equal(store.getState().value, 10);
  assert.equal(store.getVersion(), 2);
  store.undo();
  store.undo();
  assert.equal(store.getState().value, 0);
});

test('读者快照：事务中间态不可见，快照被冻结', () => {
  const store = makeStore();
  store.commit([add('base', 100)]);
  const seen: number[] = [];
  const peek: Command<Counter> = {
    name: 'peek',
    execute() {
      seen.push(store.getState().value);
    },
    undo() {},
  };
  store.commit([add('a', 1), peek, add('b', 1)]);
  assert.deepEqual(seen, [100]);
  assert.equal(store.getState().value, 102);
  const snapshot = store.getState();
  assert.throws(() => {
    (snapshot as { value: number }).value = 999;
  }, TypeError);
  store.commit([add('c', 1)]);
  assert.equal(snapshot.value, 102, '旧快照不受后续提交影响');
});

test('分支历史互不覆盖', () => {
  const store = makeStore();
  store.commit([add('base', 1)]);
  store.branch('feature');
  store.checkout('feature');
  store.commit([add('f', 10)]);
  store.checkout('main');
  store.commit([add('m', 100)]);
  assert.equal(store.getState().value, 101);
  assert.deepEqual(
    store.history('feature').map((c) => c.commands[0].name),
    ['f', 'base'],
  );
  assert.deepEqual(
    store.history('main').map((c) => c.commands[0].name),
    ['m', 'base'],
  );
  store.checkout('feature');
  assert.equal(store.getState().value, 11);
  store.undo();
  assert.equal(store.getState().value, 1);
  store.checkout('main');
  assert.equal(store.getState().value, 101, 'main 不受 feature 撤销影响');
});

test('状态可回放：replay 重建结果与当前提交状态一致', () => {
  const store = makeStore();
  store.commit([add('a', 1), add('b', 2)]);
  store.branch('dev');
  store.checkout('dev');
  store.commit([add('c', 3)]);
  assert.deepEqual(store.replay(), store.getState());
  assert.deepEqual(store.replay('main').value, 3);
});

test('checkout 后撤销基于该分支版本，跨分支旧提交撤销冲突', () => {
  const store = makeStore();
  const first = store.commit([add('a', 1)]);
  store.branch('dev');
  store.checkout('dev');
  store.commit([add('b', 2)]);
  assert.throws(() => store.undoCommit(first.id), VersionConflictError);
  assert.equal(store.getState().value, 3);
});
