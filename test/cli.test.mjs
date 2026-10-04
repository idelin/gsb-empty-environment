import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { writeFileSync, mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { samples } from '../dist/index.js';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const CLI = join(root, 'bin', 'tracecheck.mjs');

function run(args) {
  try {
    const stdout = execFileSync('node', [CLI, ...args], { encoding: 'utf8' });
    return { code: 0, stdout };
  } catch (err) {
    return { code: err.status, stdout: err.stdout || '', stderr: err.stderr || '' };
  }
}

export function testCliCheckViolationExit1() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const file = join(dir, 'trace.json');
  writeFileSync(file, JSON.stringify(samples.doubleChosen()));
  const r = run(['check', file]);
  assert.equal(r.code, 1);
  const report = JSON.parse(r.stdout);
  assert.equal(report.status, 'violations');
  assert.equal(report.violations[0].kind, 'conflicting_chosen');
}

export function testCliCheckOkExit0() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const file = join(dir, 'trace.json');
  writeFileSync(file, JSON.stringify(samples.legalNotificationDelay()));
  const r = run(['check', file]);
  assert.equal(r.code, 0);
  assert.equal(JSON.parse(r.stdout).status, 'ok');
}

export function testCliCheckIncompleteExit4() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const file = join(dir, 'trace.json');
  writeFileSync(file, JSON.stringify(samples.missingLogs()));
  const r = run(['check', file]);
  assert.equal(r.code, 4);
  assert.equal(JSON.parse(r.stdout).status, 'incomplete');
}

export function testCliEmitEvidenceAndVerify() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const file = join(dir, 'trace.json');
  const evDir = join(dir, 'ev');
  writeFileSync(file, JSON.stringify(samples.doubleChosen()));
  const r = run(['check', file, '--emit-evidence', evDir]);
  assert.equal(r.code, 1);
  const evFiles = readdirSync(evDir).filter((f) => f.endsWith('.json'));
  assert.equal(evFiles.length, 1);
  const v = run(['verify', join(evDir, evFiles[0])]);
  assert.equal(v.code, 0);
  assert.equal(JSON.parse(v.stdout).ok, true);
}

export function testCliVerifyRejectsBogusEvidence() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const file = join(dir, 'ev.json');
  writeFileSync(file, JSON.stringify({
    claim: { kind: 'conflicting_chosen', focus: ['x'], slot: 1, node: null, value: null, detail: 'bogus' },
    events: [],
  }));
  const r = run(['verify', file]);
  assert.equal(r.code, 1);
  assert.equal(JSON.parse(r.stdout).ok, false);
}

export function testCliSamplesCommand() {
  const dir = mkdtempSync(join(tmpdir(), 'tracecheck-'));
  const r = run(['samples', dir]);
  assert.equal(r.code, 0);
  const files = readdirSync(dir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.includes('doubleChosen.json'));
  assert.ok(files.includes('missingLogs.json'));
  for (const f of files) {
    const trace = JSON.parse(readFileSync(join(dir, f), 'utf8'));
    assert.ok(Array.isArray(trace) && trace.length > 0, f);
  }
}
