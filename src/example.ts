import { Store } from './store.ts';

interface Todo {
  id: number;
  text: string;
  done: boolean;
}
interface TodoState extends Record<string, unknown> {
  todos: Todo[];
  filter: 'all' | 'active' | 'done';
  nextId: number;
}

// ---- 模拟外部服务：记录真实“请求”次数，用于证明回放不发请求 ----
let httpRequests = 0;
const fakeHttp = (method: string, path: string) => {
  httpRequests++;
  return { status: 200, requestId: httpRequests };
};

const store = new Store<TodoState>({ todos: [], filter: 'all', nextId: 1 });

// 纯状态操作（可回放）
store.defineOp('addTodo', (s, text: string) => ({
  ...s,
  todos: [...s.todos, { id: s.nextId, text, done: false }],
  nextId: s.nextId + 1,
}));
store.defineOp('toggleTodo', (s, id: number) => ({
  ...s,
  todos: s.todos.map((t) => (t.id === id ? { ...t, done: !t.done } : t)),
}));
store.defineOp('setFilter', (s, filter: TodoState['filter']) => ({ ...s, filter }));

// 副作用（仅实时提交时执行一次）
store.defineEffect('http', fakeHttp);

// 派生值（按依赖缓存/重算）
store.defineDerived('remaining', ['todos'], (s) => s.todos.filter((t) => !t.done).length);
store.defineDerived('visible', ['todos', 'filter'], (s) =>
  s.filter === 'all' ? s.todos : s.todos.filter((t) => (s.filter === 'done' ? t.done : !t.done)),
);

const show = (label: string) => {
  const s = store.getState();
  console.log(
    `${label}\n  mode=${store.mode} branch=${store.branch} head=${store.head}` +
      ` | todos=${JSON.stringify(s.todos.map((t) => [t.id, t.text, t.done]))} filter=${s.filter}` +
      ` | remaining=${store.derived('remaining')} visible=${(store.derived('visible') as Todo[]).length}` +
      ` | httpRequests=${httpRequests}`,
  );
};

console.log('== 1. 实时操作（每个提交触发一次模拟 HTTP 副作用） ==');
store.transact(() => {
  store.run('addTodo', '买牛奶');
  store.emit('http', 'POST', '/todos');
});
store.transact(() => {
  store.run('addTodo', '写报告');
  store.emit('http', 'POST', '/todos');
});
store.transact(() => {
  store.run('toggleTodo', 1);
  store.run('setFilter', 'active'); // 同事务两个操作，读者只见最终态
  store.emit('http', 'PATCH', '/todos/1');
});
show('三次提交后:');

console.log('\n== 2. 回到历史（只重放纯操作，不重发请求） ==');
const firstCommit = store.history('main')[0].id;
store.checkout(firstCommit);
show(`checkout ${firstCommit}（只有 1 个 todo，filter=all）:`);
console.log(`  回看提交记录的副作用结果（不触发请求）: ${JSON.stringify(store.getCommit(firstCommit)!.effects)}`);
console.log(`  httpRequests 仍为 ${httpRequests}（回放期间没有新请求）`);

console.log('\n== 3. 在历史点上产生新操作 -> 自动分叉，不覆盖 main ==');
store.transact(() => {
  store.run('addTodo', '历史分支上的新任务');
  store.emit('http', 'POST', '/todos');
});
show('分叉提交后:');
console.log(`  分支列表: ${JSON.stringify(store.listBranches())}`);

console.log('\n== 4. 切回 main 实时模式，继续新操作 ==');
store.switchBranch('main');
show('switchBranch(main) 后:');
store.transact(() => {
  store.run('addTodo', 'main 上的后续任务');
  store.emit('http', 'POST', '/todos');
});
show('main 上再提交后:');

console.log('\n== 5. 两条分支历史均完整 ==');
for (const b of ['main', 'fork-1']) {
  const h = store.history(b);
  console.log(
    `  ${b}: ${h.map((c) => `${c.id}(ops=${c.ops.map((o) => o.op).join(',')})`).join(' <- ')}`,
  );
}
console.log(`\n派生值重算次数: remaining=${store.recomputeCount('remaining')} visible=${store.recomputeCount('visible')}`);
console.log(`总 httpRequests=${httpRequests}（= 实时提交次数，回放未产生请求）`);
