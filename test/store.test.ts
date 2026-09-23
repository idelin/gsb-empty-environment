import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createStore,
  defineSelector,
  replayOps,
  type SelectorRef,
  type Store,
  type StoreOptions,
} from '../src/index.ts';

interface Todo {
  id: string;
  title: string;
  done: boolean;
}

interface AppState {
  count: number;
  name: string;
  filter: 'active' | 'done';
  todos: Record<string, Todo>;
  lastUpdatedBy: string;
}

function makeState(): AppState {
  return {
    count: 0,
    name: 'alpha',
    filter: 'active',
    todos: {
      a: { id: 'a', title: '写文档', done: false },
      b: { id: 'b', title: '改 bug', done: true },
    },
    lastUpdatedBy: 'init',
  };
}

const sameItems = (a: Todo[], b: Todo[]): boolean =>
  a.length === b.length && a.every((item, index) => item === b[index]);

function setup(options?: StoreOptions) {
  const store = createStore<AppState>(makeState(), options);
  const root = store.root;
  const countSel = defineSelector<number>({ id: 'count', compute: (read) => read(root).count });
  const nameSel = defineSelector<string>({ id: 'name', compute: (read) => read(root).name });
  const filterSel = defineSelector<string>({ id: 'filter', compute: (read) => read(root).filter });
  const todosSel = defineSelector<Record<string, Todo>>({
    id: 'todos',
    compute: (read) => read(root).todos,
  });
  const todoById = defineSelector<Todo | undefined, string>({
    id: 'todoById',
    compute: (read, id) => read(root).todos[id],
  });
  const activeTodos = defineSelector<Todo[]>({
    id: 'activeTodos',
    equal: sameItems,
    compute: (read) => Object.values(read(root).todos).filter((todo) => !todo.done),
  });
  const doneTodos = defineSelector<Todo[]>({
    id: 'doneTodos',
    equal: sameItems,
    compute: (read) => Object.values(read(root).todos).filter((todo) => todo.done),
  });
  return {
    store,
    root,
    countSel,
    nameSel,
    filterSel,
    todosSel,
    todoById,
    activeTodos,
    doneTodos,
  };
}

function ref<T>(def: { with(params: undefined): SelectorRef<T> }): SelectorRef<T> {
  return def.with(undefined);
}

test('订阅者只收到选择器投影变化的通知', () => {
  const { store, countSel } = setup();
  const events: Array<[number, number]> = [];
  store.subscribe(ref(countSel), (next, prev) => events.push([next, prev]));

  store.update(['name'], 'beta');
  assert.equal(events.length, 0);

  store.update(['count'], 1);
  assert.deepEqual(events, [[1, 0]]);
  assert.equal(store.stats.notifications, 1);
  assert.ok(store.stats.recomputes > 0);
});

test('可配置相等函数抑制无变化通知', () => {
  const { store, root } = setup();
  const boxSel = defineSelector({
    id: 'box',
    compute: (read) => ({ count: read(root).count }),
  });
  const boxRef = ref(boxSel);
  const defaultEvents: number[] = [];
  const customEvents: number[] = [];
  store.subscribe(boxRef, () => defaultEvents.push(1));
  store.subscribe(boxRef, () => customEvents.push(1), {
    equal: (a, b) => a.count === b.count,
  });

  store.update(['name'], 'beta');
  assert.equal(defaultEvents.length, 1);
  assert.equal(customEvents.length, 0);

  store.update(['count'], 7);
  assert.equal(defaultEvents.length, 2);
  assert.equal(customEvents.length, 1);
  assert.equal(store.stats.verificationFailures, 0);
});

test('不同参数的选择器缓存相互隔离', () => {
  const { store, todoById } = setup();
  const aRef = todoById.with('a');
  const bRef = todoById.with('b');
  assert.notEqual(store.graph.instanceKey(aRef), store.graph.instanceKey(bRef));

  const beforeA = store.select(aRef);
  const beforeB = store.select(bRef);
  const eventsB: unknown[] = [];
  store.subscribe(bRef, (next) => eventsB.push(next));

  store.update(['todos', 'a', 'title'], '写文档 v2');

  const afterA = store.select(aRef);
  const afterB = store.select(bRef);
  assert.notEqual(afterA, beforeA);
  assert.equal(afterA?.title, '写文档 v2');
  assert.equal(afterB, beforeB);
  assert.equal(eventsB.length, 0);
  assert.notEqual(store.graph.cacheIdentity(aRef), store.graph.cacheIdentity(bRef));
});

test('缓存身份由参数、相等函数版本与输入依赖共同组成', () => {
  const { store, root } = setup();
  const v0 = defineSelector({ id: 'proj', equalVersion: 0, compute: (read) => read(root).count });
  const v1 = defineSelector({ id: 'proj', equalVersion: 1, compute: (read) => read(root).count });
  const ref0 = ref(v0);
  const ref1 = ref(v1);

  store.select(ref0);
  store.select(ref1);
  assert.notEqual(store.graph.instanceKey(ref0), store.graph.instanceKey(ref1));

  const identityBefore = store.graph.cacheIdentity(ref0);
  assert.match(identityBefore, /proj#eq0#/);
  assert.match(identityBefore, /root#eq0#undefined@\d+/);

  store.update(['count'], 5);
  store.select(ref0);
  const identityAfter = store.graph.cacheIdentity(ref0);
  assert.notEqual(identityBefore, identityAfter);
});

test('分支切换后撤销旧依赖', () => {
  const { store, filterSel, activeTodos, doneTodos } = setup({ verifyProjections: false });
  const filterRef = ref(filterSel);
  const activeRef = ref(activeTodos);
  const doneRef = ref(doneTodos);
  let branchComputes = 0;
  const branchSel = defineSelector<Todo[]>({
    id: 'branch',
    equal: sameItems,
    compute: (read) => {
      branchComputes++;
      return read(filterRef) === 'done' ? read(doneRef) : read(activeRef);
    },
  });
  const events: Todo[][] = [];
  store.subscribe(ref(branchSel), (next) => events.push(next));

  assert.equal(store.graph.dependentsOf(activeRef), 1);
  assert.equal(store.graph.dependentsOf(doneRef), 0);

  store.update(['filter'], 'done');
  assert.equal(events.length, 1);
  assert.equal(store.graph.dependentsOf(activeRef), 0);
  assert.equal(store.graph.dependentsOf(doneRef), 1);

  const computesBefore = branchComputes;
  store.update(['todos', 'a', 'title'], '只影响 active 分支');
  assert.equal(branchComputes, computesBefore);
  assert.equal(events.length, 1);
});

test('事务内多次变化只发布最终差量,读者看不到中间态', () => {
  const { store, countSel } = setup();
  const events: number[] = [];
  store.subscribe(ref(countSel), (next) => events.push(next));

  let countInsideTx = -1;
  store.transaction((tx) => {
    tx.set(['count'], 1);
    tx.set(['count'], 2);
    tx.set(['name'], 'gamma');
    countInsideTx = store.getState().count;
    assert.equal(events.length, 0);
  });

  assert.equal(countInsideTx, 0);
  assert.deepEqual(events, [2]);
  assert.equal(store.getState().count, 2);
  assert.equal(store.opLog.length, 1);
  assert.equal(store.opLog[0]?.length, 3);
});

test('自定义相等函数抛错被隔离,不破坏其他订阅', () => {
  const { store, countSel, nameSel } = setup();
  const eventsA: number[] = [];
  const eventsB: string[] = [];
  store.subscribe(ref(countSel), (next) => eventsA.push(next), {
    equal: () => {
      throw new Error('boom');
    },
  });
  store.subscribe(ref(nameSel), (next) => eventsB.push(next));

  store.update(['count'], 1);
  assert.deepEqual(eventsA, [1]);
  assert.equal(eventsB.length, 0);
  assert.ok(store.stats.equalityErrors >= 1);
  assert.ok(store.errors.some((entry) => entry.context.startsWith('equal:')));

  store.update(['name'], 'beta');
  assert.deepEqual(eventsA, [1, 1]);
  assert.deepEqual(eventsB, ['beta']);
  assert.equal(store.stats.listenerErrors, 0);
});

test('重入写在通知循环外排队,最终状态通过独立全量投影核验', () => {
  const { store, countSel } = setup();
  const events: number[] = [];
  store.subscribe(ref(countSel), (next) => {
    events.push(next);
    if (next === 1 && store.getState().lastUpdatedBy !== 'system') {
      store.update(['lastUpdatedBy'], 'system');
    }
  });

  store.update(['count'], 1);
  assert.deepEqual(events, [1]);
  assert.equal(store.getState().lastUpdatedBy, 'system');
  assert.equal(store.stats.verificationFailures, 0);
  assert.ok(store.stats.verifications > 0);
});

test('通知交错中取消订阅,被取消者不再收到本轮及后续通知', () => {
  const { store, countSel } = setup();
  const events1: number[] = [];
  const events2: number[] = [];
  let unsubscribe2: () => void = () => {};
  store.subscribe(ref(countSel), (next) => {
    events1.push(next);
    unsubscribe2();
  });
  unsubscribe2 = store.subscribe(ref(countSel), (next) => events2.push(next));

  store.update(['count'], 1);
  assert.deepEqual(events1, [1]);
  assert.equal(events2.length, 0);

  store.update(['count'], 2);
  assert.deepEqual(events1, [1, 2]);
  assert.equal(events2.length, 0);
  assert.equal(store.stats.verificationFailures, 0);
});

test('根对象换引用但投影相等不通知', () => {
  const { store, countSel, todosSel } = setup();
  const events: unknown[] = [];
  store.subscribe(ref(countSel), (next) => events.push(next));
  store.subscribe(ref(todosSel), (next) => events.push(next));

  store.replace({ ...store.getState() });
  assert.equal(store.stats.commits, 1);
  assert.equal(store.stats.notifications, 0);
  assert.equal(events.length, 0);
});

test('选择器返回的可变对象被深度冻结', () => {
  const { store, root } = setup();
  const objSel = defineSelector({
    id: 'obj',
    compute: (read) => ({ nested: { count: read(root).count } }),
  });
  const value = store.select(ref(objSel));
  assert.ok(Object.isFrozen(value));
  assert.ok(Object.isFrozen(value.nested));
  assert.throws(() => {
    (value as { nested: unknown }).nested = {};
  }, TypeError);
  assert.ok(Object.isFrozen(store.getState()));
  assert.ok(Object.isFrozen(store.getState().todos));
});

test('状态操作可回放:opLog 重放得到相同状态', () => {
  const { store } = setup();
  store.update(['count'], 3);
  store.transaction((tx) => {
    tx.set(['name'], 'replay');
    tx.merge(['todos', 'a'], { done: true });
  });
  store.update(['filter'], 'done');

  const ops = store.opLog.flat();
  assert.equal(store.opLog.length, 3);
  const rebuilt = replayOps(makeState(), ops);
  assert.deepEqual(rebuilt, store.getState());

  const store2 = createStore<AppState>(makeState());
  store2.replay(ops);
  assert.deepEqual(store2.getState(), store.getState());
});

test('独立全量投影核验能检测非纯选择器的不一致', () => {
  let tick = 0;
  const impure = defineSelector({ id: 'impure', compute: () => ++tick });
  const store = createStore<AppState>(makeState());
  store.subscribe(ref(impure), () => {});

  store.update(['count'], 1);
  assert.ok(store.stats.verificationFailures >= 1);
  assert.ok(store.errors.some((entry) => entry.context === 'verify'));
});

test('统计记录重算与通知数量', () => {
  const { store, countSel } = setup();
  let notifications = 0;
  store.subscribe(ref(countSel), () => notifications++);
  const recomputesAfterSubscribe = store.stats.recomputes;
  assert.ok(recomputesAfterSubscribe > 0);

  store.update(['count'], 1);
  store.update(['count'], 2);
  store.update(['name'], 'delta');

  assert.equal(notifications, 2);
  assert.equal(store.stats.notifications, 2);
  assert.ok(store.stats.recomputes > recomputesAfterSubscribe);
  assert.equal(store.stats.commits, 3);
});
