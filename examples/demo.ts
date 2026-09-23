import { createStore, defineSelector, replayOps, type Op } from '../src/index.ts';

interface Todo {
  id: string;
  title: string;
  done: boolean;
}

interface AppState {
  filter: 'active' | 'done';
  todos: Record<string, Todo>;
  lastUpdatedBy: string;
}

const initialState: AppState = {
  filter: 'active',
  todos: {
    a: { id: 'a', title: '写文档', done: false },
    b: { id: 'b', title: '改 bug', done: true },
    c: { id: 'c', title: '发版本', done: false },
  },
  lastUpdatedBy: 'init',
};

const sameItems = (a: Todo[], b: Todo[]): boolean =>
  a.length === b.length && a.every((item, index) => item === b[index]);

const store = createStore<AppState>(initialState, {
  onError: (error, context) => console.log(`  [onError] ${context}: ${(error as Error).message}`),
});
const root = store.root;

const filterSel = defineSelector({ id: 'filter', compute: (read) => read(root).filter });
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
const visibleTodos = defineSelector<Todo[]>({
  id: 'visibleTodos',
  equal: sameItems,
  compute: (read) =>
    read(filterSel.with(undefined)) === 'done'
      ? read(doneTodos.with(undefined))
      : read(activeTodos.with(undefined)),
});
const todoById = defineSelector<Todo | undefined, string>({
  id: 'todoById',
  compute: (read, id) => read(root).todos[id],
});
const summary = defineSelector({
  id: 'summary',
  compute: (read) =>
    `${read(activeTodos.with(undefined)).length} 个待办 / ${read(doneTodos.with(undefined)).length} 个已完成`,
});

const show = (label: string): void => {
  const titles = store.select(visibleTodos.with(undefined)).map((todo) => todo.title);
  console.log(`${label}: 可见=[${titles.join(', ')}] | ${store.select(summary.with(undefined))}`);
};

console.log('=== 1. 订阅选择器投影,非投影变化不通知 ===');
store.subscribe(visibleTodos.with(undefined), (next) => {
  console.log(`  [通知] visibleTodos 变化 -> [${next.map((todo) => todo.title).join(', ')}]`);
});
store.subscribe(summary.with(undefined), (next) => console.log(`  [通知] summary 变化 -> ${next}`));
show('初始');

console.log('\n=== 2. 事务内多次修改,只发布最终差量 ===');
store.transaction((tx) => {
  tx.set(['todos', 'a', 'title'], '写文档(已修订)');
  tx.set(['todos', 'b', 'done'], false);
  tx.set(['lastUpdatedBy'], 'tx-1');
  console.log(`  事务内读取 getState().lastUpdatedBy = ${store.getState().lastUpdatedBy}(仍是提交前状态)`);
});
show('事务提交后');

console.log('\n=== 3. 参数化选择器:共享子图,缓存按参数隔离 ===');
console.log(`  todoById('a') 缓存键: ${store.graph.instanceKey(todoById.with('a'))}`);
console.log(`  todoById('b') 缓存键: ${store.graph.instanceKey(todoById.with('b'))}`);
console.log(`  todoById('a') = ${JSON.stringify(store.select(todoById.with('a')))}`);

console.log('\n=== 4. 分支切换:撤销旧依赖 ===');
console.log(`  切换前 activeTodos 被依赖数 = ${store.graph.dependentsOf(activeTodos.with(undefined))}`);
store.update(['filter'], 'done');
console.log(`  切换后 activeTodos 被依赖数 = ${store.graph.dependentsOf(activeTodos.with(undefined))}`);
console.log(`  切换后 doneTodos 被依赖数 = ${store.graph.dependentsOf(doneTodos.with(undefined))}`);
show('切换到 done');

console.log('\n=== 5. 根对象换引用但投影相等,不通知 ===');
const notificationsBefore = store.stats.notifications;
store.replace({ ...store.getState() });
console.log(`  本次提交新增通知数 = ${store.stats.notifications - notificationsBefore}`);

console.log('\n=== 6. 自定义相等函数抛错:隔离并 fail-open ===');
store.subscribe(todoById.with('c'), (next) => console.log(`  [通知] todo c 变化 -> ${next?.title}`), {
  equal: () => {
    throw new Error('自定义相等函数故障');
  },
});
store.update(['todos', 'c', 'title'], '发版本 v2');

console.log('\n=== 7. 状态操作可回放 ===');
const ops: Op[] = store.opLog.flat();
const rebuilt = replayOps(initialState, ops);
console.log(`  重放 ${ops.length} 个操作后状态一致: ${JSON.stringify(rebuilt) === JSON.stringify(store.getState())}`);

console.log('\n=== 8. 统计(重算/通知/核验) ===');
console.log(`  ${JSON.stringify(store.stats)}`);
