/**
 * End-to-end demo of the acceptance boundary:
 *
 *   1. RM process receives PREPARE, writes the prepared log, locks the key.
 *   2. The process is KILLED (SIGKILL) — a hard crash, no cleanup.
 *   3. A new RM process starts on the same data dir and recovers:
 *      the prepared promise survives and the lock is still held.
 *   4. Only an explicit COMMIT from the coordinator releases the lock
 *      and persists the data.
 */
import { spawn, spawnSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';

const node = process.execPath;
const child = path.resolve('demo/child.ts');
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'xa-demo-'));

function run(cmd: string): string {
  const r = spawnSync(node, ['--experimental-strip-types', child, cmd, dir], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`'${cmd}' failed: ${r.stderr}`);
  return r.stdout.trim();
}

console.log(`data dir: ${dir}\n`);

console.log('--- step 1: PREPARE in a dedicated RM process ---');
const proc = spawn(node, ['--experimental-strip-types', child, 'prepare', dir]);
await new Promise<void>((resolve) => {
  proc.stdout.on('data', (d) => {
    process.stdout.write(`  [rm#1] ${d}`);
    resolve();
  });
});

console.log('\n--- step 2: SIGKILL the RM process (crash!) ---');
proc.kill('SIGKILL');
await new Promise((r) => proc.on('exit', (code, signal) => {
  console.log(`  rm#1 exited: code=${code} signal=${signal}`);
  r(undefined);
}));

console.log('\n--- step 3: restart RM, recover from WAL ---');
for (const line of run('check').split('\n')) console.log(`  [rm#2] ${line}`);

console.log('\n--- step 4: coordinator sends explicit COMMIT ---');
for (const line of run('commit').split('\n')) console.log(`  [rm#3] ${line}`);

console.log('\n--- step 5: verify final durable state ---');
for (const line of run('verify').split('\n')) console.log(`  [rm#4] ${line}`);

console.log('\nDemo complete: prepared promise survived a SIGKILL; lock held until explicit COMMIT.');
