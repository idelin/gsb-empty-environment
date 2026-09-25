import { DocumentStore } from '../src/store.ts';
import { compilePath } from '../src/plan.ts';
import { PlanCursor } from '../src/vm.ts';
import { PathError } from '../src/errors.ts';

function show(title: string, fn: () => unknown): void {
  console.log(`\n== ${title}`);
  try {
    console.log(fn());
  } catch (e) {
    if (e instanceof PathError) {
      console.log(`PathError ${e.code}: ${e.message}`);
    } else {
      throw e;
    }
  }
}

const store = new DocumentStore({
  users: [
    { name: 'ada', age: 36, roles: ['admin', 'dev'] },
    { name: 'grace', age: 28, roles: ['dev'] },
    { name: 'linus', age: 17, roles: [] },
  ],
  meta: { owner: { name: 'root-team' }, tags: ['core'] },
});

show('basic path', () => store.query('users[0].name').matches);
show('wildcard + filter (adults)', () =>
  store.query('users[?(@.age >= 18)].name').matches.map((m) => m.value));
show('finite set + boolean ops', () =>
  store.query(`users[?(@.name in {'ada','grace'} && !(@.age < 30))].name`).matches.map((m) => m.value));
show('recursive descent ..name', () =>
  store.query('..name').matches.map((m) => [m.path.join('.'), m.value]));

show('cache: second query hits cache', () => {
  store.query('meta.owner.name');
  const r = store.query('meta.owner.name');
  return { fromCache: r.fromCache, version: r.version };
});

show('local change revises only affected entries', () => {
  store.query('users[0].age');
  store.set(['meta', 'owner', 'name'], 'core-team'); // unrelated local change
  return {
    version: store.version,
    usersAgeCached: store.query('users[0].age').fromCache,
    ownerCached: store.query('meta.owner.name').fromCache,
    ownerNow: store.query('meta.owner.name').matches[0]?.value,
  };
});

show('pausable plan (3 micro-steps per slice)', () => {
  const cursor = new PlanCursor(compilePath('users[*].age'), store.document, 10_000);
  const slices: number[] = [];
  const out: unknown[] = [];
  while (!cursor.run(3)) {
    slices.push(cursor.stepsUsed);
    out.push(...cursor.takeMatches().map((m) => m.value));
  }
  out.push(...cursor.takeMatches().map((m) => m.value));
  return { slices, out, totalSteps: cursor.stepsUsed };
});

show('budget exceeded -> error carries the user path', () => {
  store.query('..name', 3);
});

show('__proto__ access denied at parse time', () => {
  store.query('meta.__proto__');
});

show('prototype properties are invisible (0 matches, no error)', () =>
  store.query('users[0].hasOwnProperty').matches.length);

show('constructor access is denied outright', () => {
  store.query('users[0].constructor');
});

show('array hole vs explicit undefined', () => {
  const sparse: unknown[] = new Array(2);
  const dense: unknown[] = [undefined, undefined];
  const s = new DocumentStore({ a: sparse, b: dense });
  return {
    hole: s.query('a[0]').matches.length,
    explicitUndefined: s.query('b[0]').matches.map((m) => String(m.value)),
  };
});

show('getter is never triggered', () => {
  let called = 0;
  const doc: Record<string, unknown> = { safe: 1 };
  Object.defineProperty(doc, 'trap', {
    enumerable: true,
    get() {
      called++;
      return 99;
    },
  });
  const s = new DocumentStore(doc);
  const matches = s.query('*').matches.map((m) => m.path);
  return { matches, getterCalls: called };
});
