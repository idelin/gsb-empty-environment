import { execFileSync } from 'node:child_process';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test, assert } from './harness.js';

declare const process: any;

const CLI = new URL('../../bin/ccd.js', import.meta.url).pathname;

function runCli (args: string[], stdin: string | undefined) {
  return JSON.parse(execFileSync(process.execPath, [CLI, ...args], {
    input: stdin,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  }));
}

test('cli: scene file with batches, exact output', () => {
  const dir = mkdtempSync(join(tmpdir(), 'ccd-test-'));
  const scene = {
    T: '3',
    objects: [
      { id: 'a', x: 0, y: 0, w: 1, h: 1, vx: 0, vy: 0 },
      { id: 'b', x: 2, y: 0, w: 1, h: 1, vx: -1, vy: 0 },
      { id: 'c', x: 9, y: 9, w: 1, h: 1, vx: 0, vy: 0 },
    ],
    updates: [
      { remove: ['c'], upsert: [{ id: 'd', x: '1/2', y: 5, w: 1, h: 1, vx: 0, vy: '-5/3' }] },
    ],
  };
  const file = join(dir, 'scene.json');
  writeFileSync(file, JSON.stringify(scene));
  const out = runCli([file], undefined);
  assert.strictEqual(out.T, '3');
  assert.strictEqual(out.bodies, 3);
  assert.strictEqual(out.verified, true);
  assert.strictEqual(out.candidatePairs, out.exactPairs);
  assert.strictEqual(out.exactPairs, 3);
  const ab = out.collisions.find((c: any) => c.pair[0] === 'a' && c.pair[1] === 'b');
  const ad = out.collisions.find((c: any) => c.pair[0] === 'a' && c.pair[1] === 'd');
  const bd = out.collisions.find((c: any) => c.pair[0] === 'b' && c.pair[1] === 'd');
  assert.deepStrictEqual(ab.interval, ['1', '3']);
  assert.deepStrictEqual(ad.interval, ['12/5', '3']);
  assert.deepStrictEqual(bd.interval, ['12/5', '5/2']);
});

test('cli: reads stdin and reports t=T contact', () => {
  const scene = JSON.stringify({
    T: 2,
    objects: [
      { id: 'p', x: 0, y: 0, w: 0, h: 0, vx: 1, vy: 0 },
      { id: 'q', x: 4, y: 0, w: 1, h: 1, vx: -1, vy: 0 },
    ],
  });
  const out = runCli([], scene);
  assert.strictEqual(out.verified, true);
  assert.deepStrictEqual(out.collisions[0].interval, ['2', '2']);
});

test('cli: --cell-size override keeps results exact', () => {
  const scene = JSON.stringify({
    T: '7/2',
    objects: [
      { id: 'a', x: '-3/2', y: 0, w: 1, h: 1, vx: 1, vy: 0 },
      { id: 'b', x: 2, y: '1/3', w: 1, h: 1, vx: -1, vy: 0 },
    ],
  });
  const coarse = runCli([], scene);
  const fine = runCli(['--cell-size', '1/16'], scene);
  assert.deepStrictEqual(coarse.collisions, fine.collisions);
  assert.strictEqual(fine.verified, true);
  assert.strictEqual(fine.collisions.length, 1);
  assert.deepStrictEqual(fine.collisions[0].interval, ['5/4', '9/4']);
});

test('cli: invalid input exits nonzero with error message', () => {
  let code = 0;
  try {
    execFileSync(process.execPath, [CLI], { input: '{"objects": []}', encoding: 'utf8' });
  } catch (err) {
    code = 1;
  }
  assert.strictEqual(code, 1);
});
