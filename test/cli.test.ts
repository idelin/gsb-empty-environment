import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, writeFileSync, copyFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { validateScript } from '../src/validate.ts';
import { replay } from '../src/simulator.ts';
import type { Script } from '../src/model.ts';

const cliPath = new URL('../src/cli.ts', import.meta.url).pathname;
const examplePath = new URL('../examples/failing-script.json', import.meta.url).pathname;

function runCli(args: string[]): { stdout: string } {
  const stdout = execFileSync(process.execPath, [cliPath, ...args], { encoding: 'utf8' });
  return { stdout };
}

test('shrink writes a 1-minimal failing script and a replay log', () => {
  const dir = mkdtempSync(join(tmpdir(), 'shrinker-'));
  const scriptPath = join(dir, 'script.json');
  const outPath = join(dir, 'out.json');
  const logPath = join(dir, 'replays.jsonl');
  copyFileSync(examplePath, scriptPath);

  runCli(['shrink', scriptPath, '--out', outPath, '--log', logPath]);

  const shrunk = JSON.parse(readFileSync(outPath, 'utf8')) as Script;
  assert.deepEqual(validateScript(shrunk), []);
  assert.equal(replay(shrunk).outcome, 'failed');
  assert.ok(shrunk.steps.length < 6);

  const lines = readFileSync(logPath, 'utf8').trim().split('\n');
  const summaries = lines.map((l) => JSON.parse(l));
  assert.ok(summaries.length >= shrunk.steps.length + 2);
  assert.ok(summaries.every((s) => typeof s.scriptHash === 'string' && s.scriptHash.length > 0));
  assert.ok(summaries.some((s) => s.kind === 'verify'));
  assert.ok(summaries.filter((s) => s.accepted).every((s) => s.valid && s.outcome === 'failed'));
});

test('replay command reports the failure', () => {
  const { stdout } = runCli(['replay', examplePath]);
  assert.match(stdout, /FAILED at step "cooldown"/);
});

test('shrink exits non-zero for a non-failing script', () => {
  const dir = mkdtempSync(join(tmpdir(), 'shrinker-'));
  const scriptPath = join(dir, 'passing.json');
  writeFileSync(
    scriptPath,
    JSON.stringify({ nodes: ['A', 'B'], steps: [{ id: 's1', type: 'delay', ms: 2 }] }),
  );
  assert.throws(() => runCli(['shrink', scriptPath]), (error: unknown) => {
    const err = error as { status: number; stderr: string };
    assert.equal(err.status, 3);
    assert.match(err.stderr, /does not reproduce/);
    return true;
  });
});

test('shrink exits with code 2 for an invalid script', () => {
  const dir = mkdtempSync(join(tmpdir(), 'shrinker-'));
  const scriptPath = join(dir, 'broken.json');
  writeFileSync(
    scriptPath,
    JSON.stringify({ nodes: ['A'], steps: [{ id: 's1', type: 'restart', node: 'B' }] }),
  );
  assert.throws(() => runCli(['shrink', scriptPath]), (error: unknown) => {
    const err = error as { status: number; stderr: string };
    assert.equal(err.status, 2);
    assert.match(err.stderr, /not executable/);
    return true;
  });
  assert.ok(!existsSync(join(dir, 'broken.shrunk.json')));
});
