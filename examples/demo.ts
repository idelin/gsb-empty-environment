import { decodeMessage, encodeMessage } from '../src/codec.ts';
import { validateValue } from '../src/validate.ts';
import { TruncatedFieldError } from '../src/wire/errors.ts';
import { Arbiter } from '../src/arbiter.ts';
import { Person, samplePerson } from '../test/person.ts';

const toHex = (buf: Uint8Array) => [...buf].map((b) => b.toString(16).padStart(2, '0')).join(' ');

console.log('=== 1. Protobuf wire-format codec ===');
const bytes = encodeMessage(Person, samplePerson);
console.log(`encoded ${bytes.length} bytes:`);
console.log(toHex(bytes));

const decoded = decodeMessage(Person, bytes);
console.log('\ndecoded structure:');
console.dir(decoded, { depth: null });

console.log('\n=== 2. Reflection validation engine ===');
const issues = validateValue(Person, decoded);
console.log(`validation issues: ${issues.length === 0 ? 'none' : JSON.stringify(issues)}`);
const badIssues = validateValue(Person, { id: 1n, scores: [1, 'two'], address: { zip: 'x' } });
console.log(`invalid value issues: ${badIssues.map((i) => `${i.path} (${i.message})`).join('; ')}`);

console.log('\n=== 3. Truncation safety guard ===');
const truncated = bytes.subarray(0, bytes.length - 3);
try {
  decodeMessage(Person, truncated);
  console.log('unexpected: decoded truncated buffer');
} catch (err) {
  if (err instanceof TruncatedFieldError) {
    console.log(`blocked: ${err.message}`);
  } else {
    throw err;
  }
}

console.log('\n=== 4. Multi-node conflict arbiter ===');
const arbiter = new Arbiter();
const nodes = 8;
const perNode = 20;
await Promise.all(
  Array.from({ length: nodes }, (_, i) =>
    (async () => {
      for (let n = 0; n < perNode; n++) {
        await arbiter.update(`node-${i}`, 'counter', (v) => ((v as number | undefined) ?? 0) + 1);
      }
    })(),
  ),
);
console.log(`counter after ${nodes} nodes x ${perNode} increments: ${arbiter.valueOf('counter')}`);
console.log(`version: ${arbiter.versionOf('counter')}, pending ops: ${arbiter.pendingCount}`);

const stale = arbiter.versionOf('counter');
const [winner, loser] = await Promise.all([
  arbiter.execute('node-x', { kind: 'cas', key: 'counter', expectedVersion: stale, value: -1 }),
  arbiter.execute('node-y', { kind: 'cas', key: 'counter', expectedVersion: stale, value: -2 }),
]);
console.log(`CAS race -> winner ok=${winner.ok}, loser conflict=${loser.conflict} (version now ${loser.version})`);
console.log(`history entries recorded: ${arbiter.history.length}, leaks: ${arbiter.pendingCount === 0 ? 'none' : 'LEAK'}`);
