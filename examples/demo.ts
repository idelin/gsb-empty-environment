/**
 * Runnable tour of the projection store. Execute with: npm run example
 */
import { createStore } from '../src/store.ts';
import { createSelector, createSelectorFamily, fromState } from '../src/selector.ts';

interface Todo { id: string; title: string; done: boolean }
interface State { todos: Record<string, Todo>; filter: 'all' | 'open'; ui: { theme: string } }

const initial: State = {
  todos: {
    t1: { id: 't1', title: 'write docs', done: false },
    t2: { id: 't2', title: 'ship release', done: false },
  },
  filter: 'all',
  ui: { theme: 'dark' },
};

// verify: independently recomputes every full projection after each commit.
const store = createStore<State>(structuredClone(initial), { freeze: true, verify: true });

// --- selectors -------------------------------------------------------------
const todos = fromState((s: State) => s.todos);
const openCount = createSelector(
  [todos],
  (all: Record<string, Todo>) => Object.values(all).filter((t) => !t.done).length,
);
// Parameterized family: one cache identity per (params, equalityVersion, inputs).
const todoById = createSelectorFamily((id: string) =>
  createSelector([todos], (all: Record<string, Todo>) => all[id]),
);
// Custom equality: only care about the title, ignore other field changes.
const titleOf = createSelectorFamily(
  (id: string) =>
    createSelector([todoById.for(id)], (t: Todo) => t, {
      equality: (a: Todo, b: Todo) => a.title === b.title,
      equalityVersion: 'title-v1',
    }),
);

// --- subscriptions ---------------------------------------------------------
console.log('== subscriptions ==');
store.subscribe(openCount, (v, prev) => console.log(`openCount: ${prev} -> ${v}`));
store.subscribe(titleOf.for('t1'), (v, prev) =>
  console.log(`t1 title: ${prev?.title} -> ${v.title}`),
);
// Reentrant write: when everything is done, switch the filter automatically.
store.subscribe(openCount, (v) => {
  if (v === 0 && store.getState().filter !== 'open') {
    console.log('(reentrant) all done -> switching filter to "open"');
    store.set(['filter'], 'open');
  }
});
store.subscribe(fromState((s: State) => s.filter), (v, prev) =>
  console.log(`filter: ${prev} -> ${v}`),
);

console.log('\n== one transaction, three mutations, one notification ==');
store.transaction((tx) => {
  tx.set(['todos', 't1', 'done'], true);
  tx.set(['todos', 't2', 'done'], true);
  tx.set(['ui', 'theme'], 'light'); // unrelated to every selector
});

console.log('\n== equality suppression: same title, new object ==');
store.set(['todos', 't1'], { id: 't1', title: 'write docs', done: true });

console.log('\n== root replaced by an equal projection: no notifications ==');
store.replace(structuredClone(store.getState()));

console.log('\n== replaying the action log reproduces the state ==');
const replayed = createStore<State>(structuredClone(initial));
replayed.replay(store.actionLog);
console.log('replay matches:', JSON.stringify(replayed.getState()) === JSON.stringify(store.getState()));

console.log('\n== stats (recomputes / notifications are recorded) ==');
console.log(store.stats);
console.log('openCount recomputes:', store.nodeStats(openCount).recomputes);
console.log('verification failures:', store.stats.verificationFailures);
