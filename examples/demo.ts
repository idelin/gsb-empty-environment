/**
 * Demo: persist a reactive store to a JSON checkpoint, then simulate a
 * process restart and hydrate from it.
 *
 *   node examples/demo.ts write    # process 1: mutate + save checkpoint
 *   node examples/demo.ts restore  # process 2: hydrate + verify
 */

import { existsSync } from 'node:fs';
import { Store, persist } from '../src/store.ts';

const CHECKPOINT_FILE = new URL('./checkpoint.json', import.meta.url).pathname;

function buildApp(label: string) {
  const store = new Store();

  // @persist annotated signals — serialized into checkpoints.
  const counter = persist(store, 'counter', 0);
  const user = store.persisted('user', { name: 'anonymous', visits: 0 });

  // Transient signals — never serialized, reset on hydration.
  const sessionId = store.signal('sessionId', crypto.randomUUID());

  // Derived chain.
  const doubled = store.computed(() => counter.get() * 2);
  const banner = store.computed(
    () => `[${label}] ${user.get().name} visits=${user.get().visits} counter=${counter.get()} doubled=${doubled.get()}`,
  );

  store.effect(() => console.log(`  effect fired -> ${banner.get()} (session=${sessionId.get().slice(0, 8)})`));

  return { store, counter, user, sessionId };
}

function write(): void {
  console.log('=== process 1: mutate state and save checkpoint ===');
  const app = buildApp('proc-1');
  app.counter.set(21);
  app.user.set({ name: 'ada', visits: 3 });
  app.store.saveCheckpoint(CHECKPOINT_FILE);
  console.log(`checkpoint written to ${CHECKPOINT_FILE}`);
}

function restore(): void {
  console.log('=== process 2: restart and hydrate from checkpoint ===');
  if (!existsSync(CHECKPOINT_FILE)) {
    console.error('no checkpoint found, run `node examples/demo.ts write` first');
    process.exit(1);
  }
  const app = buildApp('proc-2');
  const sessionBefore = app.sessionId.get();

  const report = app.store.hydrateFromFile(CHECKPOINT_FILE);

  console.log('hydration report:', JSON.stringify(report, null, 2));
  console.log(`persisted counter restored : ${app.counter.get()} (expect 21)`);
  console.log(`persisted user restored    : ${JSON.stringify(app.user.get())}`);
  console.log(`transient sessionId reset  : ${app.sessionId.get() !== sessionBefore ? 'no change (already initial)' : 'fresh initial value kept'}`);
}

const mode = process.argv[2];
if (mode === 'write') write();
else if (mode === 'restore') restore();
else {
  console.log('usage: node examples/demo.ts <write|restore>');
  process.exit(1);
}
