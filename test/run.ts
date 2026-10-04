import * as assert from 'assert';
import {
  Action,
  checkPolicy,
  Decision,
  FileFacts,
  interpret,
  matchGlob,
  PolicyEngine,
  Rule,
} from '../src';

let passed = 0;
function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    console.error(`FAIL ${name}`);
    console.error(err);
    process.exitCode = 1;
  }
}

function decided(d: Decision, action: Action, ruleId?: string): void {
  assert.strictEqual(d.kind, 'decided', `expected decided, got ${JSON.stringify(d)}`);
  if (d.kind === 'decided') {
    assert.strictEqual(d.action, action);
    if (ruleId !== undefined) assert.strictEqual(d.ruleId, ruleId);
  }
}

// ---------------------------------------------------------------------------
// Glob matching
// ---------------------------------------------------------------------------

test('glob: ** crosses directories, * stays within one segment', () => {
  assert.ok(matchGlob('photos/**', 'photos/a/b/c.jpg'));
  assert.ok(matchGlob('photos/**', 'photos/x.jpg'));
  assert.ok(!matchGlob('photos/*', 'photos/a/b.jpg'));
  assert.ok(matchGlob('*.tmp', 'a.tmp'));
  assert.ok(!matchGlob('*.tmp', 'dir/a.tmp'));
  assert.ok(matchGlob('**/*.tmp', 'dir/a.tmp'));
  assert.ok(matchGlob('src/?.ts', 'src/a.ts'));
});

// ---------------------------------------------------------------------------
// Priority-based interpretation
// ---------------------------------------------------------------------------

test('interpret: highest priority matching rule wins', () => {
  const rules: Rule[] = [
    { id: 'low', priority: 1, condition: { path: '**/*.jpg' }, action: 'backup' },
    { id: 'high', priority: 10, condition: { path: 'photos/**' }, action: 'exclude' },
  ];
  decided(interpret(rules, { path: 'photos/a.jpg' }), 'exclude', 'high');
  decided(interpret(rules, { path: 'other/a.jpg' }), 'backup', 'low');
});

test('interpret: no matching rule falls back to the default action', () => {
  const rules: Rule[] = [
    { id: 'r', priority: 1, condition: { path: '*.jpg' }, action: 'backup' },
  ];
  const d = interpret(rules, { path: 'a.txt' });
  assert.deepStrictEqual(d, { kind: 'default', action: 'exclude' });
});

// ---------------------------------------------------------------------------
// Unknown facts: kept as unknown, never defaulted to exclude
// ---------------------------------------------------------------------------

test('unknown: missing tags keep the outcome unknown instead of excluding', () => {
  const rules: Rule[] = [
    { id: 'tagged', priority: 5, condition: { tagsAll: ['important'] }, action: 'backup' },
  ];
  const d = interpret(rules, { path: 'a.txt', size: 10 }); // tags unknown
  assert.strictEqual(d.kind, 'unknown');
  if (d.kind === 'unknown') assert.deepStrictEqual(d.pendingRuleIds, ['tagged']);
});

test('unknown: higher-priority unknown rule blocks a lower-priority decision', () => {
  const rules: Rule[] = [
    { id: 'maybe', priority: 10, condition: { size: { min: 1000 } }, action: 'backup' },
    { id: 'sure', priority: 1, condition: { path: '**/*.log' }, action: 'exclude' },
  ];
  // size unknown -> 'maybe' is undecidable and outranks 'sure'
  const d = interpret(rules, { path: 'x/y.log' });
  assert.strictEqual(d.kind, 'unknown');
});

test('unknown: lower-priority unknown rule cannot block a higher-priority decision', () => {
  const rules: Rule[] = [
    { id: 'sure', priority: 10, condition: { path: '**/*.log' }, action: 'exclude' },
    { id: 'maybe', priority: 1, condition: { size: { min: 1000 } }, action: 'backup' },
  ];
  decided(interpret(rules, { path: 'x/y.log' }), 'exclude', 'sure');
});

test('unknown: missing size and version are undecidable, empty tags are not', () => {
  const sizeRule: Rule[] = [
    { id: 's', priority: 1, condition: { size: { min: 5 } }, action: 'backup' },
  ];
  assert.strictEqual(interpret(sizeRule, { path: 'f' }).kind, 'unknown');
  assert.strictEqual(interpret(sizeRule, { path: 'f', size: 1 }).kind, 'default');

  const tagRule: Rule[] = [
    { id: 't', priority: 1, condition: { tagsAll: ['x'] }, action: 'backup' },
  ];
  // tags present but empty -> definitely does not match
  assert.strictEqual(interpret(tagRule, { path: 'f', tags: [] }).kind, 'default');
  // tags missing entirely -> unknown
  assert.strictEqual(interpret(tagRule, { path: 'f' }).kind, 'unknown');
});

// ---------------------------------------------------------------------------
// Boundary sizes and versions (inclusive ranges)
// ---------------------------------------------------------------------------

test('boundary: size min/max are inclusive', () => {
  const rules: Rule[] = [
    { id: 'r', priority: 1, condition: { size: { min: 100, max: 200 } }, action: 'backup' },
  ];
  decided(interpret(rules, { path: 'f', size: 100 }), 'backup', 'r');
  decided(interpret(rules, { path: 'f', size: 200 }), 'backup', 'r');
  assert.strictEqual(interpret(rules, { path: 'f', size: 99 }).kind, 'default');
  assert.strictEqual(interpret(rules, { path: 'f', size: 201 }).kind, 'default');
});

test('boundary: version min/max are inclusive', () => {
  const rules: Rule[] = [
    { id: 'v', priority: 1, condition: { version: { min: 2, max: 4 } }, action: 'backup' },
  ];
  decided(interpret(rules, { path: 'f', version: 2 }), 'backup', 'v');
  decided(interpret(rules, { path: 'f', version: 4 }), 'backup', 'v');
  assert.strictEqual(interpret(rules, { path: 'f', version: 1 }).kind, 'default');
  assert.strictEqual(interpret(rules, { path: 'f', version: 5 }).kind, 'default');
  assert.strictEqual(interpret(rules, { path: 'f' }).kind, 'unknown');
});

// ---------------------------------------------------------------------------
// Negated conditions
// ---------------------------------------------------------------------------

test('negation: tagsNone excludes tagged files, stays unknown when tags missing', () => {
  const rules: Rule[] = [
    { id: 'n', priority: 1, condition: { path: '**/*', tagsNone: ['tmp'] }, action: 'backup' },
  ];
  assert.strictEqual(interpret(rules, { path: 'a', tags: ['tmp'] }).kind, 'default');
  decided(interpret(rules, { path: 'a', tags: ['keep'] }), 'backup', 'n');
  assert.strictEqual(interpret(rules, { path: 'a' }).kind, 'unknown');
});

test('negation: pathNot vetoes matching paths', () => {
  const rules: Rule[] = [
    { id: 'p', priority: 1, condition: { path: 'data/**', pathNot: '**/*.bak' }, action: 'backup' },
  ];
  decided(interpret(rules, { path: 'data/a.txt' }), 'backup', 'p');
  assert.strictEqual(interpret(rules, { path: 'data/a.bak' }).kind, 'default');
});

// ---------------------------------------------------------------------------
// Static conflict checking
// ---------------------------------------------------------------------------

test('static: same-priority overlapping rules with different actions are ambiguous', () => {
  const issues = checkPolicy([
    { id: 'a', priority: 5, condition: { size: { min: 0, max: 100 } }, action: 'backup' },
    { id: 'b', priority: 5, condition: { size: { min: 50, max: 200 } }, action: 'exclude' },
  ]);
  assert.ok(issues.some((i) => i.kind === 'ambiguous' && i.severity === 'error'));
});

test('static: disjoint ranges do not conflict', () => {
  const issues = checkPolicy([
    { id: 'a', priority: 5, condition: { size: { min: 0, max: 100 } }, action: 'backup' },
    { id: 'b', priority: 5, condition: { size: { min: 101 } }, action: 'exclude' },
  ]);
  assert.deepStrictEqual(issues, []);
});

test('static: tagsAll vs tagsNone on the same tag cannot overlap', () => {
  const issues = checkPolicy([
    { id: 'a', priority: 5, condition: { tagsAll: ['x'] }, action: 'backup' },
    { id: 'b', priority: 5, condition: { tagsNone: ['x'] }, action: 'exclude' },
  ]);
  assert.deepStrictEqual(issues, []);
});

test('static: literal paths that differ do not overlap', () => {
  const issues = checkPolicy([
    { id: 'a', priority: 5, condition: { path: 'x/a.txt' }, action: 'backup' },
    { id: 'b', priority: 5, condition: { path: 'x/b.txt' }, action: 'exclude' },
  ]);
  assert.deepStrictEqual(issues, []);
});

test('static: higher-priority rule fully covering another shadows it', () => {
  const issues = checkPolicy([
    { id: 'wide', priority: 10, condition: { size: { min: 0, max: 1000 } }, action: 'exclude' },
    { id: 'narrow', priority: 1, condition: { size: { min: 100, max: 200 } }, action: 'backup' },
  ]);
  assert.ok(issues.some((i) => i.kind === 'shadowed' && i.severity === 'warning'));
});

test('static: self-contradictory rule is unsatisfiable', () => {
  const issues = checkPolicy([
    { id: 'bad', priority: 1, condition: { size: { min: 10, max: 5 } }, action: 'backup' },
    { id: 'bad2', priority: 1, condition: { tagsAll: ['x'], tagsNone: ['x'] }, action: 'backup' },
  ]);
  assert.strictEqual(issues.filter((i) => i.kind === 'unsatisfiable').length, 2);
});

test('static: duplicate rule ids are reported', () => {
  const issues = checkPolicy([
    { id: 'dup', priority: 1, condition: {}, action: 'backup' },
    { id: 'dup', priority: 2, condition: {}, action: 'exclude' },
  ]);
  assert.ok(issues.some((i) => i.kind === 'duplicate-id'));
});

// ---------------------------------------------------------------------------
// Incremental policy updates
// ---------------------------------------------------------------------------

const POLICY_V1: Rule[] = [
  { id: 'no-tmp', priority: 100, condition: { path: '**/*.tmp' }, action: 'exclude' },
  { id: 'small', priority: 10, condition: { size: { max: 1000 } }, action: 'backup' },
];

const FILES: FileFacts[] = [
  { path: 'a.jpg', size: 2000, tags: ['photo'] },          // v1: default exclude
  { path: 'b.tmp', size: 5000, tags: ['tmp'] },            // v1: excluded by no-tmp
  { path: 'c.dat', size: 150 },                            // v1: backup via small; tags unknown
  { path: 'e.bin', size: 5000, tags: [] },                 // v1: default exclude, stable
];

const POLICY_V2: Rule[] = [
  // 'no-tmp' removed; 'small' max tightened 1000 -> 100; 'photos' added
  { id: 'small', priority: 10, condition: { size: { max: 100 } }, action: 'backup' },
  { id: 'photos', priority: 50, condition: { tagsAll: ['photo'] }, action: 'backup' },
];

test('incremental: only possibly-affected files are recomputed', () => {
  const engine = new PolicyEngine(POLICY_V1);
  engine.upsertFiles(FILES);
  const plan = engine.updatePolicy(POLICY_V2);
  // e.bin matches nothing before or after and cannot match any changed rule
  assert.ok(!plan.recomputed.includes('e.bin'), `unexpected recompute: ${plan.recomputed}`);
  assert.deepStrictEqual(plan.recomputed, ['a.jpg', 'b.tmp', 'c.dat']);
});

test('incremental: plan reports new downloads and revocations', () => {
  const engine = new PolicyEngine(POLICY_V1);
  engine.upsertFiles(FILES);
  const plan = engine.updatePolicy(POLICY_V2);

  // a.jpg: was default-excluded, now backup via 'photos' -> new download
  assert.deepStrictEqual(plan.downloads, ['a.jpg']);
  // c.dat: was backup via 'small' (size 150 <= 1000); now size 150 > 100 and its
  // tags are unknown -> no longer backup -> revocation, and kept unknown
  assert.deepStrictEqual(plan.revocations, ['c.dat']);
  assert.deepStrictEqual(plan.unknowns, ['c.dat']);

  const c = engine.getDecision('c.dat')!;
  assert.strictEqual(c.kind, 'unknown', 'missing tags must stay unknown, not excluded');
  const b = engine.getDecision('b.tmp')!;
  assert.deepStrictEqual(b, { kind: 'default', action: 'exclude' });
});

test('incremental: state matches the full-scan reference after updates', () => {
  const engine = new PolicyEngine(POLICY_V1);
  engine.upsertFiles(FILES);
  assert.deepStrictEqual(engine.verifyAgainstReference(), []);

  engine.updatePolicy(POLICY_V2);
  assert.deepStrictEqual(engine.verifyAgainstReference(), []);

  // A third policy: restore tmp exclusion, drop the photos rule again.
  engine.updatePolicy([
    { id: 'no-tmp', priority: 100, condition: { path: '**/*.tmp' }, action: 'exclude' },
    { id: 'small', priority: 10, condition: { size: { max: 100 } }, action: 'backup' },
  ]);
  assert.deepStrictEqual(engine.verifyAgainstReference(), []);
});

test('incremental: no-op policy update recomputes nothing', () => {
  const engine = new PolicyEngine(POLICY_V1);
  engine.upsertFiles(FILES);
  const plan = engine.updatePolicy(POLICY_V1.map((r) => ({ ...r })));
  assert.deepStrictEqual(plan, { downloads: [], revocations: [], unknowns: [], recomputed: [] });
});

test('incremental: unknown outcomes become decidable when facts arrive', () => {
  const engine = new PolicyEngine([
    { id: 'tagged', priority: 5, condition: { tagsAll: ['important'] }, action: 'backup' },
  ]);
  engine.upsertFiles([{ path: 'm.txt', size: 1 }]);
  assert.strictEqual(engine.getDecision('m.txt')!.kind, 'unknown');

  engine.upsertFiles([{ path: 'm.txt', size: 1, tags: ['important'] }]);
  decided(engine.getDecision('m.txt')!, 'backup', 'tagged');
});

test('incremental: invalid updated policy is rejected before any state changes', () => {
  const engine = new PolicyEngine(POLICY_V1);
  engine.upsertFiles(FILES);
  assert.throws(() =>
    engine.updatePolicy([
      { id: 'x', priority: 1, condition: { size: { max: 10 } }, action: 'backup' },
      { id: 'y', priority: 1, condition: { size: { min: 5 } }, action: 'exclude' },
    ]),
  );
  // state untouched
  assert.deepStrictEqual(engine.verifyAgainstReference(), []);
  assert.strictEqual(engine.getRules().length, POLICY_V1.length);
});

// ---------------------------------------------------------------------------
// Randomized differential test: incremental updates vs full-scan reference
// ---------------------------------------------------------------------------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('differential: randomized policies, incremental state always equals full scan', () => {
  const rand = mulberry32(20261005);
  const pick = <T>(xs: T[]): T => xs[Math.floor(rand() * xs.length)]!;
  const tags = ['photo', 'tmp', 'important', 'archive'];

  const randomRule = (id: string): Rule => {
    const condition: Rule['condition'] = {};
    if (rand() < 0.6) condition.path = pick(['**/*.jpg', 'data/**', '*.tmp', '**/*']);
    if (rand() < 0.3) condition.pathNot = pick(['**/*.bak', 'tmp/**']);
    if (rand() < 0.6) {
      const lo = Math.floor(rand() * 500);
      condition.size = rand() < 0.5 ? { min: lo } : { min: lo, max: lo + Math.floor(rand() * 1000) };
    }
    if (rand() < 0.4) condition.version = { min: Math.floor(rand() * 3), max: 2 + Math.floor(rand() * 5) };
    if (rand() < 0.5) condition.tagsAll = [pick(tags)];
    if (rand() < 0.4) condition.tagsNone = [pick(tags)];
    return { id, priority: Math.floor(rand() * 20), condition, action: pick(['backup', 'exclude'] as const) };
  };

  const randomFile = (i: number): FileFacts => {
    const f: FileFacts = { path: `${pick(['data', 'tmp', 'x'])}/f${i}${pick(['.jpg', '.tmp', '.bak', '.txt'])}` };
    if (rand() < 0.7) f.size = Math.floor(rand() * 2000);      // sometimes missing
    if (rand() < 0.6) f.version = Math.floor(rand() * 8);      // sometimes missing
    if (rand() < 0.6) {                                        // sometimes missing
      f.tags = tags.filter(() => rand() < 0.3);
    }
    return f;
  };

  for (let round = 0; round < 30; round++) {
    const files = Array.from({ length: 25 }, (_, i) => randomFile(i));
    let rules = Array.from({ length: 6 }, (_, i) => randomRule(`r${i}`));
    if (checkPolicy(rules).some((i) => i.severity === 'error')) continue;

    const engine = new PolicyEngine(rules);
    engine.upsertFiles(files);
    assert.deepStrictEqual(engine.verifyAgainstReference(), [], `round ${round} initial`);

    for (let step = 0; step < 5; step++) {
      // mutate: drop one rule, tweak one, add one
      rules = rules.filter((_, i) => i !== Math.floor(rand() * rules.length));
      if (rules.length > 0) {
        const idx = Math.floor(rand() * rules.length);
        rules[idx] = randomRule(rules[idx]!.id);
      }
      rules.push(randomRule(`r${round}-${step}`));
      if (checkPolicy(rules).some((i) => i.severity === 'error')) continue;

      engine.updatePolicy(rules);
      assert.deepStrictEqual(
        engine.verifyAgainstReference(), [],
        `round ${round} step ${step}: incremental diverged from full-scan reference`,
      );
    }
  }
});

console.log(`\n${passed} tests passed${process.exitCode ? ' (with failures)' : ''}`);
