/**
 * 测试：小域穷举核对符号分析。
 * 覆盖：互斥条件、空区间、相邻半开端点、完全/部分遮蔽、
 *       未覆盖反例、增删规则后的增量一致性、随机对拍。
 */
import assert from 'node:assert/strict';
import { Analyzer } from '../src/analyzer.ts';
import type { Analysis } from '../src/analyzer.ts';
import type { Domain, Rule } from '../src/model.ts';
import { crossCheck } from '../src/exhaustive.ts';

let passed = 0;
function check(name: string, fn: () => void): void {
  fn();
  passed++;
  console.log(`ok - ${name}`);
}

const intDomain = (max: number): Domain => ({ attrs: [{ kind: 'int', name: 'x', min: 0, max }] });

const mixedDomain: Domain = {
  attrs: [
    { kind: 'int', name: 'x', min: 0, max: 9 },
    { kind: 'enum', name: 'c', values: ['red', 'green', 'blue'] },
  ],
};

function freshAnalysis(domain: Domain, rules: Rule[]): Analysis {
  const a = new Analyzer(domain);
  for (const r of rules) a.addRule(r);
  return a.analyze();
}

/** 两份分析在语义层面一致（状态、见证点、未覆盖计数） */
function assertSameAnalysis(a: Analysis, b: Analysis): void {
  assert.equal(a.rules.length, b.rules.length);
  a.rules.forEach((ra, i) => {
    const rb = b.rules[i];
    assert.equal(ra.status, rb.status, `rule ${i} status`);
    assert.deepEqual(ra.shadowedBy, rb.shadowedBy, `rule ${i} shadowedBy`);
    assert.deepEqual(ra.liveExample, rb.liveExample, `rule ${i} liveExample`);
    assert.deepEqual(ra.shadowedExample, rb.shadowedExample, `rule ${i} shadowedExample`);
  });
  assert.equal(a.uncoveredCount, b.uncoveredCount);
  assert.deepEqual(a.uncoveredExample, b.uncoveredExample);
}

// ---------- 相邻半开端点 ----------
check('adjacent half-open intervals tile seamlessly', () => {
  const rules: Rule[] = [
    { id: 'lo', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 5, hiOpen: true }] }, // [0,5)
    { id: 'hi', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 5, hi: 10, hiOpen: true }] }, // [5,10)
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.deepEqual(a.rules.map((r) => r.status), ['live', 'live']);
  assert.equal(a.uncoveredCount, 0);
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

check('half-open gap yields minimal uncovered counterexample at the seam', () => {
  const rules: Rule[] = [
    { id: 'lo', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 5, hiOpen: true }] }, // [0,5)
    { id: 'hi', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 6, hi: 9 }] }, // [6,9]
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.equal(a.uncoveredCount, 1);
  assert.deepEqual(a.uncoveredExample, { x: 5 });
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

check('inclusive+inclusive adjacency shadows exactly the shared endpoint', () => {
  const rules: Rule[] = [
    { id: 'lo', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 5 }] }, // [0,5]
    { id: 'hi', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 5, hi: 9 }] }, // [5,9]
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.equal(a.rules[1].status, 'partially-shadowed');
  assert.deepEqual(a.rules[1].shadowedBy, [0]);
  assert.deepEqual(a.rules[1].shadowedExample, { x: 5 });
  assert.deepEqual(a.rules[1].liveExample, { x: 6 });
  assert.equal(a.uncoveredCount, 0);
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

// ---------- 互斥条件 ----------
check('mutually exclusive int intervals never shadow each other', () => {
  const rules: Rule[] = [
    { id: 'a', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 3 }] },
    { id: 'b', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 5, hi: 7 }] },
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.deepEqual(a.rules.map((r) => r.status), ['live', 'live']);
  assert.deepEqual(a.rules[1].shadowedBy, []);
  assert.equal(a.uncoveredCount, 3); // x = 4, 8, 9
  assert.deepEqual(a.uncoveredExample, { x: 4 });
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

check('mutually exclusive enum values never shadow each other', () => {
  const rules: Rule[] = [
    { id: 'a', action: 'A', conds: [{ kind: 'enum', attr: 'c', values: ['red'] }] },
    { id: 'b', action: 'B', conds: [{ kind: 'enum', attr: 'c', values: ['blue'] }] },
  ];
  const a = freshAnalysis(mixedDomain, rules);
  assert.deepEqual(a.rules.map((r) => r.status), ['live', 'live']);
  assert.equal(a.uncoveredCount, 10); // c = green, x = 0..9
  assert.deepEqual(a.uncoveredExample, { x: 0, c: 'green' });
  assert.deepEqual(crossCheck(mixedDomain, rules, a), []);
});

// ---------- 空区间 ----------
check('empty half-open interval [5,5) is reported as empty rule', () => {
  const rules: Rule[] = [
    { id: 'empty', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 5, hi: 5, hiOpen: true }] },
    { id: 'rest', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 9 }] },
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.equal(a.rules[0].status, 'empty');
  assert.equal(a.rules[1].status, 'live'); // 空规则不遮蔽任何东西
  assert.equal(a.uncoveredCount, 0);
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

check('inverted interval [7,3] is empty', () => {
  const rules: Rule[] = [{ id: 'bad', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 7, hi: 3 }] }];
  const a = freshAnalysis(intDomain(9), rules);
  assert.equal(a.rules[0].status, 'empty');
  assert.equal(a.uncoveredCount, 10);
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

// ---------- 遮蔽 ----------
check('fully shadowed rule is unreachable with minimal stolen witness', () => {
  const rules: Rule[] = [
    { id: 'wide', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 9 }] },
    { id: 'narrow', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 2, hi: 4 }] },
  ];
  const a = freshAnalysis(intDomain(9), rules);
  assert.equal(a.rules[1].status, 'unreachable');
  assert.deepEqual(a.rules[1].shadowedBy, [0]);
  assert.deepEqual(a.rules[1].shadowedExample, { x: 2 });
  assert.equal(a.rules[1].liveExample, undefined);
  assert.deepEqual(crossCheck(intDomain(9), rules, a), []);
});

check('partial shadow across int and enum dims', () => {
  const rules: Rule[] = [
    { id: 'r1', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 5 }, { kind: 'enum', attr: 'c', values: ['red', 'green'] }] },
    { id: 'r2', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 3, hi: 8 }, { kind: 'enum', attr: 'c', values: ['green', 'blue'] }] },
  ];
  const a = freshAnalysis(mixedDomain, rules);
  assert.equal(a.rules[1].status, 'partially-shadowed');
  assert.deepEqual(a.rules[1].shadowedBy, [0]);
  assert.deepEqual(a.rules[1].shadowedExample, { x: 3, c: 'green' });
  assert.deepEqual(a.rules[1].liveExample, { x: 3, c: 'blue' });
  assert.deepEqual(crossCheck(mixedDomain, rules, a), []);
});

// ---------- 增量增删 ----------
check('incremental add equals batch analysis', () => {
  const rules: Rule[] = [
    { id: 'r0', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 4 }] },
    { id: 'r1', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 2, hi: 6 }] },
    { id: 'r2', action: 'C', conds: [{ kind: 'enum', attr: 'c', values: ['red'] }] },
    { id: 'r3', action: 'D', conds: [{ kind: 'int', attr: 'x', lo: 8, hi: 9 }] },
  ];
  const inc = new Analyzer(mixedDomain);
  let last: Analysis | undefined;
  for (const r of rules) last = inc.addRule(r);
  assertSameAnalysis(last!, freshAnalysis(mixedDomain, rules));
});

check('removing a middle rule restores coverage of later rules', () => {
  const rules: Rule[] = [
    { id: 'r0', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 9 }] },
    { id: 'r1', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 2, hi: 4 }] },
    { id: 'r2', action: 'C', conds: [{ kind: 'int', attr: 'x', lo: 3, hi: 5 }] },
  ];
  const inc = new Analyzer(mixedDomain);
  for (const r of rules) inc.addRule(r);
  const after = inc.removeRule(0);
  const expected = freshAnalysis(mixedDomain, [rules[1], rules[2]]);
  assertSameAnalysis(after, expected);
  assert.equal(after.rules[0].status, 'live'); // 原 r1 不再被遮蔽
  assert.equal(after.rules[1].status, 'partially-shadowed');
  assert.deepEqual(crossCheck(mixedDomain, [rules[1], rules[2]], after), []);
});

check('insert in the middle recomputes suffix exactly', () => {
  const base: Rule[] = [
    { id: 'r0', action: 'A', conds: [{ kind: 'int', attr: 'x', lo: 0, hi: 2 }] },
    { id: 'r2', action: 'C', conds: [{ kind: 'int', attr: 'x', lo: 1, hi: 8 }] },
  ];
  const inc = new Analyzer(intDomain(9));
  for (const r of base) inc.addRule(r);
  const inserted: Rule = { id: 'r1', action: 'B', conds: [{ kind: 'int', attr: 'x', lo: 3, hi: 5 }] };
  const after = inc.addRule(inserted, 1);
  const expected = freshAnalysis(intDomain(9), [base[0], inserted, base[1]]);
  assertSameAnalysis(after, expected);
  assert.deepEqual(crossCheck(intDomain(9), [base[0], inserted, base[1]], after), []);
});

// ---------- 随机小域对拍 ----------
check('randomized small domains: symbolic analysis matches brute force', () => {
  let seed = 0x2f6e2b1;
  const rand = (): number => {
    seed ^= seed << 13; seed ^= seed >>> 17; seed ^= seed << 5; seed >>>= 0;
    return seed / 0xffffffff;
  };
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];

  for (let trial = 0; trial < 300; trial++) {
    const domain: Domain = {
      attrs: [
        { kind: 'int', name: 'x', min: 0, max: 2 + Math.floor(rand() * 6) },
        { kind: 'enum', name: 'c', values: ['p', 'q', 'r'] },
      ],
    };
    const xmax = (domain.attrs[0] as { max: number }).max;
    const rules: Rule[] = Array.from({ length: 1 + Math.floor(rand() * 5) }, (_, i) => {
      const conds: Rule['conds'][number][] = [];
      if (rand() < 0.8) {
        const lo = Math.floor(rand() * (xmax + 2)); // 允许 lo 越界产生空区间
        const hi = Math.floor(rand() * (xmax + 2));
        conds.push({ kind: 'int', attr: 'x', lo, hi, loOpen: rand() < 0.3, hiOpen: rand() < 0.3 });
      }
      if (rand() < 0.6) {
        const vals = ['p', 'q', 'r'].filter(() => rand() < 0.6);
        conds.push({ kind: 'enum', attr: 'c', values: vals });
      }
      return { id: `r${i}`, action: `a${i}`, conds };
    });

    // 随机增删序列后，与批量分析对拍 + 穷举对拍
    const inc = new Analyzer(domain);
    const alive: Rule[] = [];
    let last: Analysis | undefined;
    for (const r of rules) {
      if (alive.length > 0 && rand() < 0.25) {
        const idx = Math.floor(rand() * alive.length);
        alive.splice(idx, 1);
        last = inc.removeRule(idx);
      }
      const at = rand() < 0.3 ? Math.floor(rand() * (alive.length + 1)) : alive.length;
      alive.splice(at, 0, r);
      last = inc.addRule(r, at);
    }
    assertSameAnalysis(last!, freshAnalysis(domain, alive));
    const problems = crossCheck(domain, alive, last!);
    assert.deepEqual(problems, [], `trial ${trial}: ${problems.join('; ')}`);
  }
});

console.log(`\n${passed} test groups passed`);
