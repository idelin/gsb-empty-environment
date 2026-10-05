import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const CLI = new URL('../src/cli.ts', import.meta.url).pathname;

function run(args: string[]): string {
  return execFileSync(process.execPath, [CLI, ...args], { encoding: 'utf8' });
}

test('cli compress/decompress round trip with size stats', () => {
  const dir = mkdtempSync(join(tmpdir(), 'lzarc-'));
  const input = join(dir, 'input.log');
  const packed = join(dir, 'packed.lz');
  const output = join(dir, 'output.log');
  const line = '2026-10-05T08:00:00 INFO worker-3 request id=999 status=ok elapsed=3ms\n';
  writeFileSync(input, line.repeat(2000));

  const compressOut = run(['compress', input, packed, '--window-bits', '16']);
  assert.match(compressOut, /ratio:/);
  assert.match(compressOut, /blocks:/);
  assert.match(compressOut, /not proof of correctness/);

  run(['decompress', packed, output]);
  assert.deepEqual(readFileSync(output), readFileSync(input));
});

test('cli verify reports a byte-exact round trip', () => {
  const dir = mkdtempSync(join(tmpdir(), 'lzarc-'));
  const input = join(dir, 'data.bin');
  const bytes = new Uint8Array(30000).map((_, i) => (i * 31 + (i >> 5)) & 0xff);
  writeFileSync(input, bytes);
  const out = run(['verify', input, '--block-size', '4096']);
  assert.match(out, /round-trip: OK \(byte-identical\)/);
});
