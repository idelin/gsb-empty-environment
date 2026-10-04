import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { test } from './harness.mjs';
import { solveAllocation } from '../dist/allocator.js';
import { projection, assertFeasible } from './util.mjs';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const cliPath = join(root, 'dist', 'cli.js');
const examplePath = join(root, 'examples', 'example.json');

function runCli(args, stdin) {
  return spawnSync(process.execPath, [cliPath, ...args], {
    input: stdin,
    encoding: 'utf8',
  });
}

test('CLI solves the example file and matches the library result', () => {
  const proc = runCli([examplePath]);
  assert.equal(proc.status, 0, proc.stderr);
  const output = JSON.parse(proc.stdout);
  const input = JSON.parse(readFileSync(examplePath, 'utf8'));
  const direct = solveAllocation(input, input.budget ?? {});
  assert.equal(projection(output.result), projection(direct));
  assert.equal(output.result.provenOptimal, true);
  assertFeasible(output.result, input);
});

test('CLI reads from stdin and honors --budget', () => {
  const input = JSON.parse(readFileSync(examplePath, 'utf8'));
  const proc = runCli(['-', '--budget', '1', '--compact'], JSON.stringify(input));
  assert.equal(proc.status, 0, proc.stderr);
  const output = JSON.parse(proc.stdout);
  assert.equal(output.result.provenOptimal, false);
  assertFeasible(output.result, input);
});

test('CLI --baseline includes the greedy plan', () => {
  const proc = runCli([examplePath, '--baseline', '--compact']);
  assert.equal(proc.status, 0, proc.stderr);
  const output = JSON.parse(proc.stdout);
  assert.ok(output.baseline);
  assert.ok(
    output.result.stats.admittedDemand >= output.baseline.stats.admittedDemand,
  );
});

test('CLI rejects input with a missing cost pair', () => {
  const bad = {
    requests: [{ id: 'r1', region: 'X', demand: 1 }],
    nodes: [{ id: 'A', region: 'Y', capacity: 1 }],
    costs: {},
  };
  const proc = runCli(['-'], JSON.stringify(bad));
  assert.equal(proc.status, 2);
  assert.match(proc.stderr, /missing cross-region cost: X -> Y/);
});

test('CLI rejects invalid JSON', () => {
  const proc = runCli(['-'], '{not json');
  assert.equal(proc.status, 2);
  assert.match(proc.stderr, /not valid JSON/);
});
