'use strict';

const fs = require('fs');
const os = require('os');
const path = require('path');

const { Builder } = require('../src/builder');
const { loadTypeScript } = require('../src/load-typescript');

const ts = loadTypeScript();

// ---------------------------------------------------------------------------
// Fixture sources
// ---------------------------------------------------------------------------

const UTIL_V1 = `export function add(a: number, b: number): number {
  return a + b;
}
`;
// Internal implementation change only; exported signature is identical.
const UTIL_V2 = `export function add(a: number, b: number): number {
  const total = a + b;
  return total;
}
`;
// Export signature change (return type number -> string).
const UTIL_V3 = `export function add(a: number, b: number): string {
  return String(a + b);
}
`;

const CONFIG = `export const config: { retries: number } = { retries: 3 };
`;

const SERVICE = `import { add } from './util';
export function compute(x: number) {
  return add(x, 1);
}
`;
// Type error: string is not assignable to number.
const SERVICE_BROKEN = `import { add } from './util';
export function compute(x: number) {
  return add(x, 1);
}
export const bad: number = 'oops';
`;

const API = `import { compute } from './service';
import { config } from './config';
export function handler() {
  return compute(config.retries);
}
`;

const CYCLE_A_V1 = `import { b } from './cycleB';
export function a(n: number): number {
  return n <= 0 ? 0 : b(n - 1) + 1;
}
`;
// Internal change only; exported signature identical.
const CYCLE_A_V2 = `import { b } from './cycleB';
export function a(n: number): number {
  if (n <= 0) {
    return 0;
  }
  return b(n - 1) + 1;
}
`;
// Export signature change (extra optional parameter).
const CYCLE_A_V3 = `import { b } from './cycleB';
export function a(n: number, flag: boolean = false): number {
  return n <= 0 ? 0 : b(n - 1) + (flag ? 2 : 1);
}
`;

const CYCLE_B = `import { a } from './cycleA';
export function b(n: number): number {
  return n <= 0 ? 0 : a(n - 1) + 1;
}
`;

const CONSUMER = `import { a } from './cycleA';
export function use(): number {
  return a(3);
}
`;

const BASE_FILES = {
  'util.ts': UTIL_V1,
  'config.ts': CONFIG,
  'service.ts': SERVICE,
  'api.ts': API,
  'cycleA.ts': CYCLE_A_V1,
  'cycleB.ts': CYCLE_B,
  'consumer.ts': CONSUMER,
};
const ALL_MODULES = Object.keys(BASE_FILES).sort();

// ---------------------------------------------------------------------------
// Harness helpers
// ---------------------------------------------------------------------------

const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'ts-build-cache-'));
const projectDir = path.join(tmpRoot, 'project');
const incCacheDir = path.join(tmpRoot, 'cache-incremental');
fs.mkdirSync(projectDir, { recursive: true });

let checkCount = 0;
let failureCount = 0;

function check(label, condition, detail) {
  checkCount += 1;
  if (condition) {
    console.log(`  PASS ${label}`);
  } else {
    failureCount += 1;
    console.log(`  FAIL ${label}${detail ? ` -- ${detail}` : ''}`);
  }
}

function writeFiles(files) {
  for (const [name, content] of Object.entries(files)) {
    fs.writeFileSync(path.join(projectDir, name), content);
  }
}

function makeIncrementalBuilder() {
  return new Builder({ ts, rootDir: projectDir, cacheDir: incCacheDir });
}

// Full rebuild against a brand-new, empty cache directory.
function fullRebuild() {
  const cacheDir = fs.mkdtempSync(path.join(tmpRoot, 'cache-full-'));
  const builder = new Builder({ ts, rootDir: projectDir, cacheDir });
  return builder.build();
}

function canonicalOutputs(result) {
  const obj = {};
  for (const [id, out] of [...result.outputs.entries()].sort()) {
    obj[id] = { js: out.js, dts: out.dts };
  }
  return obj;
}

function expectMatchesFullRebuild(label, incrementalResult) {
  const full = fullRebuild();
  check(`${label}: full rebuild compiles every module`, full.stats.compiled.join(',') === ALL_MODULES.join(','),
    `compiled=[${full.stats.compiled}]`);
  check(`${label}: full rebuild succeeds`, full.success);
  const inc = canonicalOutputs(incrementalResult);
  const ref = canonicalOutputs(full);
  check(`${label}: incremental outputs identical to full rebuild`,
    JSON.stringify(inc) === JSON.stringify(ref),
    `differing modules: ${Object.keys(ref).filter((k) => JSON.stringify(inc[k]) !== JSON.stringify(ref[k])).join(', ')}`);
}

function expectCompiled(label, result, expected) {
  const actual = result.stats.compiled.slice().sort();
  const want = expected.slice().sort();
  check(`${label}: compiled modules = [${want.join(', ')}] (count ${want.length})`,
    actual.join(',') === want.join(','),
    `actual=[${actual.join(', ')}]`);
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

console.log(`TypeScript compiler: ${ts.version}`);
console.log(`Workspace: ${tmpRoot}\n`);

// --- Scenario 1: cold build compiles everything ---------------------------
console.log('S1: cold build (empty cache)');
writeFiles(BASE_FILES);
const s1 = makeIncrementalBuilder().build();
check('S1: build succeeds', s1.success);
expectCompiled('S1', s1, ALL_MODULES);
check('S1: no cache hits', s1.stats.fromCache.length === 0);
expectMatchesFullRebuild('S1', s1);

// --- Scenario 2: warm no-op build ------------------------------------------
console.log('S2: warm build, no changes');
const s2 = makeIncrementalBuilder().build();
check('S2: build succeeds', s2.success);
expectCompiled('S2', s2, []);
check('S2: all modules served from cache', s2.stats.fromCache.join(',') === ALL_MODULES.join(','),
  `fromCache=[${s2.stats.fromCache}]`);
check('S2: outputs identical to cold build',
  JSON.stringify(canonicalOutputs(s2)) === JSON.stringify(canonicalOutputs(s1)));

// --- Scenario 3: internal change must not recompile callers ----------------
console.log('S3: internal implementation change in util.ts');
writeFiles({ 'util.ts': UTIL_V2 });
const s3 = makeIncrementalBuilder().build();
check('S3: build succeeds', s3.success);
expectCompiled('S3', s3, ['util.ts']);
check('S3: callers served from cache',
  ['service.ts', 'api.ts'].every((m) => s3.stats.fromCache.includes(m)),
  `fromCache=[${s3.stats.fromCache}]`);
expectMatchesFullRebuild('S3', s3);

// --- Scenario 4: export change invalidates transitively --------------------
console.log('S4: export signature change in util.ts (return type)');
writeFiles({ 'util.ts': UTIL_V3 });
const s4 = makeIncrementalBuilder().build();
check('S4: build succeeds', s4.success);
expectCompiled('S4', s4, ['util.ts', 'service.ts', 'api.ts']);
check('S4: unrelated modules stay cached',
  ['config.ts', 'cycleA.ts', 'cycleB.ts', 'consumer.ts'].every((m) => s4.stats.fromCache.includes(m)),
  `fromCache=[${s4.stats.fromCache}]`);
expectMatchesFullRebuild('S4', s4);

// --- Scenario 5: failed compilation never enters the cache -----------------
console.log('S5: type error in service.ts');
const keysBefore = makeIncrementalBuilder().cache.keys();
writeFiles({ 'service.ts': SERVICE_BROKEN });
const s5 = makeIncrementalBuilder().build();
check('S5: build reports failure', !s5.success);
check('S5: failed modules = [api.ts, service.ts]',
  s5.stats.failed.join(',') === 'api.ts,service.ts',
  `failed=[${s5.stats.failed}]`);
check('S5: nothing compiled into cache', s5.stats.compiled.length === 0,
  `compiled=[${s5.stats.compiled}]`);
const keysAfter = makeIncrementalBuilder().cache.keys();
check('S5: cache contents unchanged by failed build',
  JSON.stringify(keysBefore) === JSON.stringify(keysAfter),
  `before=${keysBefore.length} entries, after=${keysAfter.length} entries`);
check('S5: diagnostics recorded for service.ts',
  Array.isArray(s5.stats.errors['service.ts']) && s5.stats.errors['service.ts'].length > 0);
// Re-running the still-broken build must fail again (no poisoning, no hits).
const s5b = makeIncrementalBuilder().build();
check('S5: repeated broken build still fails, still compiles nothing',
  !s5b.success && s5b.stats.compiled.length === 0);
// Fix the error: sources return to the S4 state and must be served from cache.
writeFiles({ 'service.ts': SERVICE });
const s5c = makeIncrementalBuilder().build();
check('S5c: fixed build succeeds', s5c.success);
expectCompiled('S5c', s5c, []);
check('S5c: previously failed modules now resolve from cache',
  ['service.ts', 'api.ts'].every((m) => s5c.stats.fromCache.includes(m)));
expectMatchesFullRebuild('S5c', s5c);

// --- Scenario 6: cycle internal change recompiles only the SCC -------------
console.log('S6: internal change inside cycle (cycleA.ts)');
writeFiles({ 'cycleA.ts': CYCLE_A_V2 });
const s6 = makeIncrementalBuilder().build();
check('S6: build succeeds', s6.success);
expectCompiled('S6', s6, ['cycleA.ts', 'cycleB.ts']);
check('S6: cycle consumer served from cache', s6.stats.fromCache.includes('consumer.ts'),
  `fromCache=[${s6.stats.fromCache}]`);
expectMatchesFullRebuild('S6', s6);

// --- Scenario 7: cycle export change recompiles SCC + dependents -----------
console.log('S7: export signature change in cycleA.ts');
writeFiles({ 'cycleA.ts': CYCLE_A_V3 });
const s7 = makeIncrementalBuilder().build();
check('S7: build succeeds', s7.success);
expectCompiled('S7', s7, ['cycleA.ts', 'cycleB.ts', 'consumer.ts']);
check('S7: modules outside the cycle stay cached',
  ['util.ts', 'config.ts', 'service.ts', 'api.ts'].every((m) => s7.stats.fromCache.includes(m)),
  `fromCache=[${s7.stats.fromCache}]`);
expectMatchesFullRebuild('S7', s7);

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

console.log('\nCompile counts per scenario:');
const counts = [
  ['S1 cold build', s1],
  ['S2 no-op', s2],
  ['S3 internal change', s3],
  ['S4 export change', s4],
  ['S5 broken build', s5],
  ['S5c fixed build', s5c],
  ['S6 cycle internal', s6],
  ['S7 cycle export', s7],
];
for (const [name, result] of counts) {
  console.log(`  ${name}: compiled=${result.stats.compiled.length} cached=${result.stats.fromCache.length} failed=${result.stats.failed.length}`);
}

console.log(`\n${checkCount - failureCount}/${checkCount} checks passed`);
if (failureCount > 0) {
  console.error(`${failureCount} check(s) FAILED`);
  process.exit(1);
}
console.log('All checks passed.');
