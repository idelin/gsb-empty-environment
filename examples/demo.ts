/**
 * Runnable sample: `node examples/demo.ts` (Node >= 22.6, runs TS directly).
 */
import {
  compile,
  StructureCache,
  formatPath,
  ParseError,
  BudgetExceededError,
} from '../src/index.ts';

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  try {
    fn();
  } catch (err) {
    console.log(`error: ${(err as Error).message}`);
  }
}

const doc = {
  store: {
    book: [
      { title: 'A', price: 5, stock: 0 },
      { title: 'B', price: 12, stock: 3 },
      { title: 'C', price: 8, stock: 7 },
    ],
    bicycle: { color: 'red', price: 19 },
  },
};

show('1. basic query with evidence paths', () => {
  const plan = compile('$.store.book[?(@.price < 10 && @.stock > 0)].title');
  for (const m of plan.run(doc)) {
    console.log(`  ${formatPath(m.path)} = ${JSON.stringify(m.value)}`);
  }
});

show('2. recursive descent + finite set filter', () => {
  const plan = compile("$..book[?(@.title in ['A', 'C'])].price");
  console.log('  prices:', plan.run(doc).map((m) => m.value));
});

show('3. resumable plan: pause and resume with step slices', () => {
  const plan = compile('$.store.book[*].title');
  const execution = plan.start(doc);
  let r = execution.resume(2);
  console.log(`  after 2 steps: done=${r.done}, steps=${r.totalSteps}`);
  r = execution.resume(1000);
  console.log(`  after resume: done=${r.done}, steps=${r.totalSteps}, values=${JSON.stringify(r.matches.map((m) => m.value))}`);
});

show('4. bounded execution: tiny budget raises BudgetExceededError', () => {
  compile('$..price').run(doc, { maxTotalSteps: 3 });
});

show('5. denied properties are rejected at parse time', () => {
  try {
    compile('$.store.__proto__.polluted');
  } catch (err) {
    console.log(`  rejected: ${(err as ParseError).message}`);
  }
});

show('6. getters never fire; prototype properties invisible', () => {
  let calls = 0;
  const tricky = Object.create({ inherited: 'x' });
  Object.defineProperty(tricky, 'trap', { enumerable: true, get() { calls++; return 1; } });
  tricky.safe = 42;
  console.log('  $.safe     ->', compile('$.safe').run(tricky).map((m) => m.value));
  console.log('  $.trap     ->', compile('$.trap').run(tricky).length, 'matches, getter calls =', calls);
  console.log('  $.inherited->', compile('$.inherited').run(tricky).length, 'matches');
});

show('7. array holes vs explicit undefined', () => {
  // eslint-disable-next-line no-sparse-arrays
  const arr = [1, , undefined];
  console.log('  $[*] paths            ->', compile('$[*]').run(arr).map((m) => formatPath(m.path)));
  console.log('  $[?(@ == undefined)]  ->', compile('$[?(@ == undefined)]').run(arr).map((m) => formatPath(m.path)));
  console.log('  $[?(exists(@))]       ->', compile('$[?(exists(@))]').run(arr).map((m) => formatPath(m.path)));
});

show('8. version-bound cache: local change revises results and evidence', () => {
  const shop: { items: { id: number }[] } = { items: [{ id: 1 }, { id: 2 }] };
  const cache = new StructureCache();
  const plan = compile('$.items[?(@.id == 1)]');
  console.log('  first query :', cache.query(plan, shop).result.evidence);
  console.log('  cached query:', cache.query(plan, shop).fromCache);
  shop.items.push({ id: 1 });
  cache.invalidate(shop);
  const revised = cache.query(plan, shop);
  console.log(`  after change: fromCache=${revised.fromCache}, evidence=${JSON.stringify(revised.result.evidence)}`);
});
