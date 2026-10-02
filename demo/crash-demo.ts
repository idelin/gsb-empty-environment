import { spawn } from 'node:child_process';
import { rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ResourceManager } from '../src/rm.ts';

const here = dirname(fileURLToPath(import.meta.url));
const dataDir = join(here, '..', 'demo-data');
const worker = join(here, 'rm-worker.ts');

function run(args: string[]): Promise<{ code: number | null; output: string }> {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [worker, dataDir, ...args]);
    let output = '';
    child.stdout.on('data', (chunk) => (output += chunk));
    child.stderr.on('data', (chunk) => (output += chunk));
    child.on('error', reject);
    child.on('close', (code) => resolve({ code, output }));
  });
}

function step(title: string): void {
  console.log(`\n=== ${title} ===`);
}

rmSync(dataDir, { recursive: true, force: true });

step('1. Coordinator sends Prepare; RM persists Prepared log and locks the key');
const preparer = spawn(process.execPath, [worker, dataDir, 'prepare', 'tx-1', 'account:A', '{"balance": 50}']);
let preparedLine = '';
preparer.stdout.on('data', (chunk) => {
  preparedLine += chunk;
  if (preparedLine.includes('PREPARED')) {
    process.stdout.write(`   worker says: ${preparedLine.trim()}\n`);
    step('2. SIGKILL the RM process (simulated crash after prepare)');
    preparer.kill('SIGKILL');
  }
});
await new Promise((resolve) => preparer.on('close', resolve));
console.log('   worker killed; no clean shutdown, no rollback happened');

step('3. Restart RM: state recovered, lock still held, promise intact');
const check = await run(['check', 'tx-1', 'account:A', 'tx-intruder']);
process.stdout.write(check.output.replace(/^/gm, '   '));
if (check.code !== 0) {
  console.error('check worker failed');
  process.exit(1);
}

step('4. Coordinator finally decides: explicit Commit releases lock and persists');
const commit = await run(['commit', 'tx-1']);
process.stdout.write(commit.output.replace(/^/gm, '   '));

step('5. Verify final state in a fresh process');
const rm = ResourceManager.open(dataDir);
console.log(`   status(tx-1)      = ${rm.status('tx-1')}`);
console.log(`   locked(account:A) = ${rm.isLocked('account:A')}`);
console.log(`   committed value   = ${JSON.stringify(rm.committedValue('account:A'))}`);
rm.close();

console.log('\nDemo finished: prepare promise survived the crash, commit released the lock.');
