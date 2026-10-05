import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  MEMBERS,
  legalNotificationDelayTrace,
  doubleChosenTrace,
} from './samples.ts';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const cli = join(root, 'src', 'cli.ts');

function runCli(args: string[]) {
  return spawnSync(process.execPath, [cli, ...args], { encoding: 'utf8' });
}

function writeJson(name: string, data: unknown): string {
  const dir = mkdtempSync(join(tmpdir(), 'trace-checker-'));
  const file = join(dir, name);
  writeFileSync(file, JSON.stringify(data));
  return file;
}

test('cli check: legal trace exits 0', () => {
  const file = writeJson('ok.json', { members: MEMBERS, events: legalNotificationDelayTrace() });
  const res = runCli(['check', file]);
  assert.equal(res.status, 0, res.stderr);
  const out = JSON.parse(res.stdout);
  assert.equal(out.ok, true);
});

test('cli check: double chosen exits 1 with evidence', () => {
  const file = writeJson('bad.json', { members: MEMBERS, events: doubleChosenTrace() });
  const res = runCli(['check', file]);
  assert.equal(res.status, 1, res.stderr);
  const out = JSON.parse(res.stdout);
  assert.equal(out.ok, false);
  assert.equal(out.violations[0].kind, 'ConflictingChosen');
  assert.equal(out.violations[0].evidence.minimalProven, true);
});

test('cli check: bare event array is accepted', () => {
  const file = writeJson('arr.json', doubleChosenTrace());
  const res = runCli(['check', file]);
  assert.equal(res.status, 1, res.stderr);
});

test('cli check: conflicting duplicate id exits 2', () => {
  const events = doubleChosenTrace();
  events.push({ ...events[0], value: 'tampered' });
  const file = writeJson('conflict.json', { members: MEMBERS, events });
  const res = runCli(['check', file]);
  assert.equal(res.status, 2);
  assert.match(res.stderr, /conflicting content/);
});

test('cli check: --budget 1 marks evidence as not proven minimal', () => {
  const file = writeJson('budget.json', { members: MEMBERS, events: doubleChosenTrace() });
  const res = runCli(['check', file, '--budget', '1']);
  assert.equal(res.status, 1, res.stderr);
  const out = JSON.parse(res.stdout);
  assert.equal(out.violations[0].evidence.minimalProven, false);
  assert.equal(out.violations[0].evidence.budgetExhausted, true);
});

test('cli verify: round-trips evidence produced by check', () => {
  const traceFile = writeJson('bad.json', { members: MEMBERS, events: doubleChosenTrace() });
  const check = runCli(['check', traceFile]);
  const finding = JSON.parse(check.stdout).violations[0];
  const evidenceFile = writeJson('evidence.json', {
    claim: finding.evidence.claim,
    events: finding.evidence.events,
  });
  const res = runCli(['verify', evidenceFile]);
  assert.equal(res.status, 0, res.stderr);
  assert.equal(JSON.parse(res.stdout).valid, true);
});

test('cli verify: rejects tampered evidence', () => {
  const traceFile = writeJson('bad.json', { members: MEMBERS, events: doubleChosenTrace() });
  const check = runCli(['check', traceFile]);
  const finding = JSON.parse(check.stdout).violations[0];
  const tampered = finding.evidence.events.filter((e: { id: string }) => e.id !== 'a1');
  const evidenceFile = writeJson('evidence.json', { claim: finding.evidence.claim, events: tampered });
  const res = runCli(['verify', evidenceFile]);
  assert.equal(res.status, 1);
  assert.equal(JSON.parse(res.stdout).valid, false);
});
