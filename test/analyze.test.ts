import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { validateTable, type Condition, type DecisionTable, type Scalar } from '../src/model.ts';
import { analyzeTable } from '../src/analyze.ts';
import { fieldDomain, ruleBox, boxIsEmpty } from '../src/boxes.ts';
import { toJsonReport } from '../src/report.ts';
import { bruteForce, rng } from './helpers.ts';

const example: DecisionTable = validateTable(
  JSON.parse(readFileSync(new URL('../examples/discounts.json', import.meta.url), 'utf8')),
);

test('example table: statuses', () => {
  const report = analyzeTable(example);
  const byId = new Map(report.rules.map((r) => [r.ruleId, r]));
  assert.equal(byId.get('R1-vip')!.status, 'winnable');
  assert.equal(byId.get('R2-promo')!.status, 'winnable');
  assert.equal(byId.get('R3-apac')!.status, 'winnable');
  assert.equal(byId.get('R4-youth')!.status, 'winnable');
  assert.equal(byId.get('R5-youth-dup')!.status, 'covered');
  assert.equal(byId.get('R6-broken')!.status, 'unsatisfiable');
  assert.equal(byId.get('R7-fallback')!.status, 'winnable');
});

test('example table: minimal witness for top rule', () => {
  const report = analyzeTable(example);
  const w = report.rules.find((r) => r.ruleId === 'R1-vip')!.witness!;
  assert.equal(w.constrainedFields, 2);
  assert.deepEqual(w.pattern.tier, ['gold', 'platinum']);
  assert.equal(w.pattern.age, 'any');
  assert.equal(w.pattern.region, 'any');
  const spend = w.pattern.spend as number[];
  assert.equal(spend[0], 1000);
  assert.equal(spend[spend.length - 1], 10000);
  // 2 tiers * 4 regions * 121 ages * 9001 spends
  assert.equal(w.guaranteedInputs, (2n * 4n * 121n * 9001n).toString());
});

test('example table: negation appears in witness', () => {
  const report = analyzeTable(example);
  const w = report.rules.find((r) => r.ruleId === 'R2-promo')!.witness!;
  const regions = w.pattern.region as string[];
  assert.ok(!regions.includes('LATAM'));
});

test('example table: coverage explanation', () => {
  const report = analyzeTable(example);
  const covered = report.rules.find((r) => r.ruleId === 'R5-youth-dup')!;
  assert.equal(covered.coveredBy!.length, 1);
  const cover = covered.coveredBy![0];
  assert.equal(cover.ruleId, 'R4-youth');
  assert.equal(cover.relation, 'higher-priority');
  assert.deepEqual(cover.region.age, [20, 21, 22, 23, 24, 25]);
  assert.deepEqual(cover.region.tier, ['standard']);
});

test('example table: equal-priority conflict with witness', () => {
  const report = analyzeTable(example);
  const conflict = report.conflicts.find(
    (c) => c.ruleIds.includes('R2-promo') && c.ruleIds.includes('R3-apac'),
  );
  assert.ok(conflict, 'expected R2 x R3 conflict');
  assert.equal(conflict.effective, true);
  assert.equal(conflict.witnessInput.region, 'APAC');
  const spend = conflict.witnessInput.spend as number;
  assert.ok(spend >= 500 && spend <= 9999);
  // witness hits both R2 and R3, and is not decided by higher-priority R1
  for (const [id, shouldHit] of [['R1-vip', false], ['R2-promo', true], ['R3-apac', true]] as const) {
    const rule = example.rules.find((r) => r.id === id)!;
    const box = ruleBox(example, rule);
    const hits = example.fields.every((f) => box.get(f.name)!.has(conflict.witnessInput[f.name]));
    assert.equal(hits, shouldHit, `${id} on conflict witness`);
  }
});

test('large int domains stay symbolic and exact', () => {
  const table = validateTable({
    fields: [{ name: 'n', kind: 'int', min: 0, max: 200000 }],
    rules: [
      { id: 'hi', priority: 10, conditions: { n: { op: 'range', min: 50000, max: 150000 } } },
      { id: 'lo', priority: 0, conditions: { n: { op: 'not', cond: { op: 'range', min: 1, max: 199999 } } } },
    ],
  });
  const report = analyzeTable(table);
  const hi = report.rules.find((r) => r.ruleId === 'hi')!;
  assert.equal(hi.status, 'winnable');
  assert.equal(hi.witness!.guaranteedInputs, '100001');
  const lo = report.rules.find((r) => r.ruleId === 'lo')!;
  assert.equal(lo.status, 'winnable');
  assert.equal(lo.witness!.guaranteedInputs, '2'); // only 0 and 200000
});

test('validation rejects bad tables', () => {
  assert.throws(() => validateTable({ fields: [], rules: [{ id: 'x' }] }), /fields/);
  assert.throws(
    () => validateTable({ fields: [{ name: 'a', kind: 'int', min: 5, max: 1 }], rules: [{ id: 'x' }] }),
    /min/,
  );
  assert.throws(
    () => validateTable({
      fields: [{ name: 'a', kind: 'int', min: 0, max: 1 }],
      rules: [{ id: 'x', conditions: { nope: { op: 'any' } } }],
    }),
    /unknown field/,
  );
});

test('json report compresses int ranges into runs', () => {
  const report = analyzeTable(example);
  const json = toJsonReport(report, example.fields) as {
    rules: { ruleId: string; witness?: { pattern: Record<string, unknown> } }[];
  };
  const r1 = json.rules.find((r) => r.ruleId === 'R1-vip')!;
  assert.deepEqual(r1.witness!.pattern.spend, { runs: [[1000, 10000]] });
  assert.deepEqual(r1.witness!.pattern.tier, ['gold', 'platinum']);
  assert.equal(r1.witness!.pattern.age, 'any');
});

// ---- randomized cross-check against exhaustive enumeration ----

function randomCondition(rand: () => number, intRange: [number, number]): Condition {
  const pick = rand();
  const [lo, hi] = intRange;
  const ri = () => lo + Math.floor(rand() * (hi - lo + 1));
  if (pick < 0.25) return { op: 'eq', value: ri() };
  if (pick < 0.45) {
    const a = ri();
    const b = ri();
    return { op: 'range', min: Math.min(a, b), max: Math.max(a, b) };
  }
  if (pick < 0.65) {
    const n = 1 + Math.floor(rand() * 3);
    const values: Scalar[] = [];
    for (let i = 0; i < n; i++) values.push(ri());
    return { op: 'in', values };
  }
  if (pick < 0.85) return { op: 'not', cond: randomCondition(rand, intRange) };
  return {
    op: rand() < 0.5 ? 'and' : 'or',
    conds: [randomCondition(rand, intRange), randomCondition(rand, intRange)],
  };
}

function randomTable(seed: number): DecisionTable {
  const rand = rng(seed);
  const table: DecisionTable = {
    fields: [
      { name: 'a', kind: 'int', min: 0, max: 5 },
      { name: 'b', kind: 'enum', values: ['x', 'y', 'z'] },
      { name: 'c', kind: 'int', min: 0, max: 3 },
    ],
    rules: [],
  };
  const nRules = 2 + Math.floor(rand() * 5);
  for (let i = 0; i < nRules; i++) {
    const conditions: Record<string, Condition> = {};
    for (const fname of ['a', 'b', 'c']) {
      if (rand() < 0.6) {
        if (fname === 'b') {
          const vals = ['x', 'y', 'z'].filter(() => rand() < 0.5);
          conditions[fname] = vals.length
            ? (rand() < 0.5 ? { op: 'in', values: vals } : { op: 'not', cond: { op: 'in', values: vals } })
            : { op: 'any' };
        } else {
          conditions[fname] = randomCondition(rand, fname === 'a' ? [0, 5] : [0, 3]);
        }
      }
    }
    table.rules.push({ id: `r${i}`, priority: Math.floor(rand() * 4), conditions });
  }
  return table;
}

test('random tables match brute force (300 seeds)', () => {
  for (let seed = 1; seed <= 300; seed++) {
    const table = randomTable(seed);
    const report = analyzeTable(table);
    const bf = bruteForce(table);

    // statuses
    for (const r of report.rules) {
      const idx = table.rules.findIndex((x) => x.id === r.ruleId);
      const empty = boxIsEmpty(ruleBox(table, table.rules[idx]));
      if (empty) {
        assert.equal(r.status, 'unsatisfiable', `seed ${seed} ${r.ruleId}`);
        continue;
      }
      const want = bf.winnable.has(r.ruleId) ? 'winnable' : 'covered';
      assert.equal(r.status, want, `seed ${seed} ${r.ruleId}`);
      if (r.status === 'covered') assert.ok(r.coveredBy!.length > 0);
    }

    // witness inputs really win, and every pattern completion wins
    for (const r of report.rules) {
      if (r.status !== 'winnable') continue;
      const w = r.witness!;
      assert.equal(bf.winnerOf(w.input), r.ruleId, `seed ${seed} ${r.ruleId} witness`);
      let expected = 1n;
      const domains = table.fields.map((f) => {
        const spec = w.pattern[f.name];
        const values = spec === 'any' ? fieldDomain(f) : spec;
        expected *= BigInt(values.length);
        return { name: f.name, values };
      });
      assert.equal(BigInt(w.guaranteedInputs), expected);
      const input: Record<string, Scalar> = {};
      const walk = (fi: number) => {
        if (fi === domains.length) {
          assert.equal(
            bf.winnerOf({ ...input }),
            r.ruleId,
            `seed ${seed} ${r.ruleId} pattern completion ${JSON.stringify(input)}`,
          );
          return;
        }
        for (const v of domains[fi].values) {
          input[domains[fi].name] = v;
          walk(fi + 1);
        }
      };
      walk(0);
    }

    // effective conflicts match exactly; every conflict witness hits both rules
    const got = new Set(
      report.conflicts.filter((c) => c.effective).map((c) => [...c.ruleIds].sort().join('|')),
    );
    assert.deepEqual(got, bf.effectiveConflicts, `seed ${seed} conflicts`);
    for (const c of report.conflicts) {
      for (const id of c.ruleIds) {
        const idx = table.rules.findIndex((x) => x.id === id);
        const box = ruleBox(table, table.rules[idx]);
        for (const f of table.fields) {
          assert.ok(
            box.get(f.name)!.has(c.witnessInput[f.name]),
            `seed ${seed} conflict witness ${id} field ${f.name}`,
          );
        }
      }
    }
  }
});
