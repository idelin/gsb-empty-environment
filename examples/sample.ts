import { PolicyEngine, Registry } from '../src/index.ts';
import type { ModuleSpec } from '../src/index.ts';

// --- 1. Diamond shared imports -------------------------------------------------
const registry = new Registry();
registry.register({
  name: 'logger', version: '1',
  exports: { log: { kind: 'value', signature: '(string) => void' } },
  imports: [],
  init: () => ({ log: (msg: string) => console.log(`  [logger@1] ${msg}`) }),
});
const makeConsumer = (name: string): ModuleSpec => ({
  name, version: '1',
  exports: { run: { kind: 'value', signature: '() => void' } },
  imports: [{ from: 'logger', version: '1', names: { log: '(string) => void' } }],
  init: (ns) => ({ run: () => (ns.logger as { log: (m: string) => void }).log(`hello from ${name}`) }),
});
registry.register(makeConsumer('left'));
registry.register(makeConsumer('right'));
registry.register({
  name: 'app', version: '1',
  exports: { main: { kind: 'value', signature: '() => void' } },
  imports: [
    { from: 'left', version: '1', names: { run: '() => void' } },
    { from: 'right', version: '1', names: { run: '() => void' } },
  ],
  init: (ns) => ({
    main: () => {
      (ns.left as { run: () => void }).run();
      (ns.right as { run: () => void }).run();
    },
  }),
});

console.log('== diamond link ==');
const linked = registry.link([{ name: 'app', version: '1' }]);
if (linked.ok) {
  console.log(`  linked vector: ${linked.cacheKey}`);
  (linked.records.get('app')!.exports.main as () => void)();
}

// --- 2. Policy evaluation -------------------------------------------------------
console.log('== policy ==');
const policies = new PolicyEngine();
policies.register('auth', {
  name: 'canPublish',
  when: {
    op: 'and',
    conditions: [
      { op: 'in', prop: 'role', set: ['admin', 'editor'] },
      { op: 'ge', prop: 'level', value: 2 },
    ],
  },
  effect: 'allow',
});
console.log(`  admin/3  -> ${policies.evaluate('auth', 'canPublish', { role: 'admin', level: 3 })}`);
console.log(`  viewer/9 -> ${policies.evaluate('auth', 'canPublish', { role: 'viewer', level: 9 })}`);

// --- 3. Hot replacement with rollback -------------------------------------------
console.log('== hmr ==');
registry.register({
  name: 'logger', version: '2',
  exports: { log: { kind: 'value', signature: '(string) => void' } },
  imports: [],
  init: () => ({ log: (msg: string) => console.log(`  [logger@2] ${msg}`) }),
});
registry.register({
  name: 'left', version: '2',
  exports: { run: { kind: 'value', signature: '() => void' } },
  imports: [{ from: 'logger', version: '2', names: { log: '(string) => void' } }],
  init: (ns) => ({ run: () => (ns.logger as { log: (m: string) => void }).log('hello from left@2') }),
});
const swapped = registry.hmrReplace([{ name: 'left', version: '2' }]);
if (swapped.ok) {
  console.log(`  swapped: ${JSON.stringify(swapped.previous.live)} -> ${JSON.stringify(swapped.current.live)}`);
  (registry.getExport('left', 'run') as () => void)();
}

registry.register({
  name: 'broken', version: '1',
  exports: { x: { kind: 'value', signature: '() => void' } },
  imports: [],
  init: () => { throw new Error('cannot start'); },
});
const failed = registry.hmrReplace([{ name: 'broken', version: '1' }]);
if (!failed.ok) {
  console.log(`  rollback after failure: ${failed.errors[0].message}`);
  console.log(`  live set still: ${JSON.stringify(failed.snapshot.live)}`);
}
