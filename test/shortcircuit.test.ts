import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { and, eq, oneOf } from '../src/policy.ts';
import { plan } from '../src/planner.ts';
import { AttributeScheduler, type AttrSource, type FetchResult } from '../src/scheduler.ts';
import { FileEvidenceStore, MemoryEvidenceStore } from '../src/evidence.ts';

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));

function recordingSource(values: Record<string, number>, version = 1) {
  const calls: { subject: string; attrs: string[]; scope: string }[] = [];
  const source: AttrSource = {
    async fetch(subject, attrs, scope): Promise<FetchResult> {
      calls.push({ subject, attrs: [...attrs], scope });
      return { version, values: Object.fromEntries(attrs.map((a) => [a, values[a]])) };
    },
  };
  return { source, calls };
}

test('deny decided by a false cheap condition never requests the sensitive expensive field', async () => {
  // ssn_score is listed first in the policy text but is 100x more expensive.
  const policy = and(oneOf('ssn_score', [10, 20, 30]), eq('risk_flag', 1));
  const costs = { risk_flag: 1, ssn_score: 100 };
  const { source, calls } = recordingSource({ risk_flag: 0, ssn_score: 10 });
  const evidence = new MemoryEvidenceStore();
  const scheduler = new AttributeScheduler(source, evidence, { costs });

  const decision = await scheduler.evaluate(policy, 'alice', 'scope-standard');

  assert.equal(decision.allow, false);
  assert.deepEqual(
    calls.flatMap((c) => c.attrs),
    ['risk_flag'],
    'sensitive expensive attribute must not be requested once deny is decided',
  );
  assert.deepEqual(
    decision.accesses.map((a) => a.attr),
    ['risk_flag'],
  );
  // evidence and decision committed together, exactly once
  assert.equal(evidence.records.length, 1);
  assert.equal(evidence.records[0].allow, false);
  assert.deepEqual(evidence.records[0].accesses, decision.accesses);
  // nothing further is fetched after completion
  await tick();
  assert.equal(calls.length, 1);
});

test('planner distinguishes must-read from optional attributes', () => {
  const policy = and(oneOf('ssn_score', [10, 20, 30]), eq('risk_flag', 1));
  const steps = plan(policy, { risk_flag: 1, ssn_score: 100 });
  assert.deepEqual(
    steps.map((s) => [s.attr, s.required]),
    [
      ['risk_flag', true], // cheapest first: must be read to start deciding
      ['ssn_score', false], // optional: skipped when risk_flag already decides
    ],
  );
});

test('decision and access evidence are saved atomically as one record', async () => {
  const dir = mkdtempSync(join(tmpdir(), 'evidence-'));
  const policy = and(oneOf('ssn_score', [10]), eq('risk_flag', 1));
  const { source } = recordingSource({ risk_flag: 0, ssn_score: 10 });
  const scheduler = new AttributeScheduler(source, new FileEvidenceStore(dir), {
    costs: { risk_flag: 1, ssn_score: 100 },
  });

  const decision = await scheduler.evaluate(policy, 'alice', 'scope-standard', { decisionId: 'case-1' });

  const files = await fs.readdir(dir);
  assert.deepEqual(files.sort(), ['case-1.json'], 'no leftover temp files, exactly one record');
  const saved = JSON.parse(await fs.readFile(join(dir, 'case-1.json'), 'utf8'));
  assert.equal(saved.allow, false);
  assert.deepEqual(saved.accesses, decision.accesses, 'evidence saved together with the decision');
  assert.equal(saved.subject, 'alice');
  assert.equal(saved.scope, 'scope-standard');
});
