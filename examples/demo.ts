import {
  interpretFullLog,
  LogBuilder,
  LogCorruptionError,
  RecoveryEngine,
  type LogRecord,
} from '../src/index.ts';
import type { JsonValue } from '../src/state.ts';

const line = (s = '') => console.log(s);

line('=== 1. Original run: build the operation log ===');
const effectCalls: string[] = [];
const b = new LogBuilder();
b.set('user:1', { name: 'ada' });
b.set('user:2', { name: 'grace' });
b.begin('tx-transfer');
b.set('acct:a', 100);
b.set('acct:b', -100);
b.commit();
b.createBranch('feature');
b.switchBranch('feature');
b.set('flag:x', true);
b.effect('email-1', { to: 'ada@example.com' }, (id) => {
  effectCalls.push(id);
  return { sent: true };
});
b.checkpoint('cp1');
b.switchBranch('main');
b.begin('tx-temp');
b.set('tmp', 1);
b.abort();
b.del('user:2');
line(`log records: ${b.length}, side effects executed: [${effectCalls}]`);
line(`original state digest: ${b.state.digest()}`);

line();
line('=== 2. Crash & recover: step-by-step replay from the serialized log ===');
const serialized = b.serialize(); // the only thing that survives the "crash"
const replayCalls: string[] = [];
const engine = new RecoveryEngine({
  handler: (id: string) => {
    replayCalls.push(id);
    return null;
  },
});
engine.load(serialized);
let outcome;
while (!(outcome = engine.step()).done) {
  const note = 'note' in outcome && outcome.note ? `  # ${outcome.note}` : '';
  line(`  applied seq ${String(outcome.seq).padStart(2)} ${outcome.type}${note}`);
}
line(`recovered digest:      ${engine.digest()}`);
line(`interpreter digest:    ${interpretFullLog(b.getRecords()).digest}`);
line(`side effects re-executed during replay: [${replayCalls}] (acked effects are not re-triggered)`);
line(`digests equal: ${engine.digest() === b.state.digest()}`);

line();
line('=== 3. Rollback via inverse patches ===');
for (let i = 0; i < 5; i++) engine.rollback();
line(`after 5 reverse steps, cursor=${engine.cursorSeq}, keys=${JSON.stringify(Object.keys(engine.getState()))}`);
engine.runToEnd();
line(`re-stepped to end, digest matches original: ${engine.digest() === b.state.digest()}`);

line();
line('=== 4. Checkpoint save / reclaim / restore ===');
const mid = new RecoveryEngine();
mid.load(serialized);
for (let i = 0; i < 6; i++) mid.step();
mid.checkpoint('after-tx');
mid.runToEnd();
mid.checkpoint('final');
line(`checkpoints before reclaim: ${mid.checkpointCount}, reclaimed: ${mid.reclaimCheckpoints()}`);
const resumed = new RecoveryEngine();
resumed.load(serialized);
const cp = mid.checkpoint('final-bound');
resumed.restore(cp);
line(`restored checkpoint at seq ${cp.seq}, digest matches: ${resumed.digest() === b.state.digest()}`);

line();
line('=== 5. Corruption is located to its operation seq ===');
const lines = serialized.trimEnd().split('\n');
const rec = JSON.parse(lines[8]) as LogRecord;
rec.payload = { forged: true };
lines[8] = JSON.stringify(rec);
try {
  new RecoveryEngine().load(lines.join('\n') + '\n');
} catch (e) {
  if (e instanceof LogCorruptionError) line(`rejected: ${e.message}`);
}

line();
line('=== 6. Cancel an in-progress recovery ===');
const partial = new RecoveryEngine();
partial.load(serialized);
for (let i = 0; i < 4; i++) partial.step();
line(`keys before cancel: ${JSON.stringify(Object.keys(partial.getState()))}`);
partial.cancel();
line(`keys after cancel:  ${JSON.stringify(Object.keys(partial.getState()))}`);
try {
  partial.step();
} catch (e) {
  line(`step after cancel rejected: ${(e as Error).name}`);
}
