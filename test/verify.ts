/**
 * Verification suite.
 *
 * 1. Targeted cases with hand-computed optima (including tie-breaks where a
 *    naive "first parseable wins" search would return a wrong answer).
 * 2. Exhaustive cross-check: every token string up to length 4 over small
 *    alphabets is repaired and compared against the brute-force oracle, which
 *    enumerates *all* minimum-cost edit sequences (never the first parseable
 *    one) and takes the lexicographically smallest.
 * 3. Seeded fuzzing over the full terminal set, cross-checked at low cost.
 * 4. Budget / bound behaviour and position preservation.
 */

import type { Token, TokenKind } from '../src/tokens.ts';
import { lex, canonicalText, TERMINALS } from '../src/tokens.ts';
import type { Edit } from '../src/edits.ts';
import { compareEditSeqs } from '../src/edits.ts';
import { parses } from '../src/parser.ts';
import { repair } from '../src/repair.ts';
import { bruteForceRepair } from '../src/bruteforce.ts';

let failures = 0;
let checks = 0;

function check(cond: boolean, msg: string): void {
  checks++;
  if (!cond) {
    failures++;
    console.error(`FAIL: ${msg}`);
  }
}

function tokensOf(kinds: TokenKind[]): Token[] {
  return kinds.map((kind, i) => ({ kind, text: canonicalText(kind), pos: i }));
}

function showEdits(edits: Edit[]): string {
  return edits.map((e) => `${e.type === 'delete' ? 'D' : 'I'}@${e.index}:${e.kind}`).join(' ');
}

/** Cross-check one input against the brute-force oracle. */
function crossCheck(kinds: TokenKind[], bruteMaxCost: number, label: string): void {
  const input = tokensOf(kinds);
  const r = repair(input);
  check(r.status === 'ok', `${label}: expected ok, got ${JSON.stringify(r)}`);
  if (r.status !== 'ok') return;

  // The repaired stream must parse (independent parser) and cost = #edits.
  const outKinds = r.tokens.map((t) => t.kind);
  check(parses(outKinds), `${label}: repaired stream does not parse: ${outKinds.join(' ')}`);
  check(r.cost === r.edits.length, `${label}: cost ${r.cost} != ${r.edits.length} edits`);

  // Original tokens keep their positions and relative order.
  const kept = r.tokens.filter((t) => t.pos !== null);
  check(
    kept.every((t) => {
      const orig = input[Number(t.pos)];
      return orig !== undefined && orig.kind === t.kind && orig.text === t.text;
    }) && kept.every((t, i, a) => i === 0 || Number(a[i - 1].pos) < Number(t.pos)),
    `${label}: original positions not preserved`,
  );

  const brute = bruteForceRepair(kinds, Math.min(bruteMaxCost, r.cost));
  if (r.cost <= bruteMaxCost) {
    check(brute !== null, `${label}: brute force found nothing but repair cost ${r.cost}`);
    if (brute) {
      check(
        brute.cost === r.cost,
        `${label}: cost mismatch repair=${r.cost} brute=${brute.cost} [${showEdits(r.edits)} vs ${showEdits(brute.edits)}]`,
      );
      check(
        compareEditSeqs(brute.edits, r.edits) === 0,
        `${label}: lex-min mismatch repair=[${showEdits(r.edits)}] brute=[${showEdits(brute.edits)}]`,
      );
    }
  } else {
    check(brute === null, `${label}: brute force found cost ${brute?.cost} but repair says ${r.cost}`);
  }
}

// ---------- 1. targeted cases ----------

function expectRepair(src: string, cost: number, edits: Edit[]): void {
  const r = repair(lex(src));
  check(r.status === 'ok', `'${src}': expected ok`);
  if (r.status !== 'ok') return;
  check(r.cost === cost, `'${src}': cost ${r.cost}, want ${cost}`);
  check(
    compareEditSeqs(r.edits, edits) === 0,
    `'${src}': edits [${showEdits(r.edits)}], want [${showEdits(edits)}]`,
  );
}

expectRepair('0+0*0', 0, []);
expectRepair('a(0,[0+0])', 0, []);
expectRepair('', 1, [{ type: 'insert', index: 0, kind: 'int' }]);
expectRepair('+', 2, [
  { type: 'delete', index: 0, kind: '+' },
  { type: 'insert', index: 1, kind: 'int' },
]);
// Two min-cost fixes exist ("delete one int" x2, "insert operator"); the
// lexicographically smallest sequence deletes the *first* token.
expectRepair('0 0', 1, [{ type: 'delete', index: 0, kind: 'int' }]);
expectRepair('[0,0', 1, [{ type: 'insert', index: 4, kind: ']' }]);
expectRepair('a(', 1, [{ type: 'insert', index: 2, kind: ')' }]);
// "delete ','" (D@3) beats "insert int" (I@4) although both cost 1.
expectRepair('a(0,)', 1, [{ type: 'delete', index: 3, kind: ',' }]);
// "delete ')'" (D@1) beats "insert '(' at 0" (I@0): D < I globally.
expectRepair('0)', 1, [{ type: 'delete', index: 1, kind: ')' }]);
// Cost-2 repairs: delete both '(' leaving '0', or delete one '(' and insert
// ')' giving '(0)', or insert ')' twice. [D@0, D@1] is lexicographically
// smallest because the second delete (D@1) beats any insert (I@...).
expectRepair('((0', 2, [
  { type: 'delete', index: 0, kind: '(' },
  { type: 'delete', index: 1, kind: '(' },
]);

// ---------- 2. exhaustive short-string cross-check ----------

function* allStrings(alphabet: TokenKind[], maxLen: number): Generator<TokenKind[]> {
  yield [];
  const current: TokenKind[] = [];
  function* rec(): Generator<TokenKind[]> {
    if (current.length === maxLen) return;
    for (const kind of alphabet) {
      current.push(kind);
      yield [...current];
      yield* rec();
      current.pop();
    }
  }
  yield* rec();
}

function exhaustive(alphabet: TokenKind[], maxLen: number, bruteMaxCost: number): void {
  let count = 0;
  for (const kinds of allStrings(alphabet, maxLen)) {
    crossCheck(kinds, bruteMaxCost, `exhaustive[${kinds.join(' ')}]`);
    count++;
  }
  console.log(`exhaustive: ${count} strings over {${alphabet.join(' ')}} up to length ${maxLen}`);
}

exhaustive(['int', '+', '(', ')'], 4, 3);
exhaustive(['int', 'ident', '(', ')', ','], 3, 3);
exhaustive(['int', '[', ']', ','], 3, 3);
exhaustive(['int', '+', '-', '*', '/'], 3, 3);

// ---------- 3. seeded fuzzing over the full terminal set ----------

function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rand = mulberry32(20261005);
for (let i = 0; i < 300; i++) {
  const len = Math.floor(rand() * 9);
  const kinds: TokenKind[] = [];
  for (let j = 0; j < len; j++) kinds.push(TERMINALS[Math.floor(rand() * TERMINALS.length)]);
  crossCheck(kinds, 2, `fuzz#${i}[${kinds.join(' ')}]`);
}
console.log('fuzz: 300 random strings over all terminals, cross-checked at cost <= 2');

// ---------- 4. budget, bounds, positions ----------

{
  const input = lex('0 + )');
  const r = repair(input);
  check(r.status === 'ok' && r.cost === 2, `'0 + )': cost 2 expected`);
  if (r.status === 'ok') {
    check(r.tokens.length === 1 && r.tokens[0].pos === 0, `'0 + )': original position preserved`);
  }
}

{
  // True cost of '0 +' is 1 (insert an integer at the end).
  for (let budget = 0; budget < 40; budget++) {
    const r = repair(lex('0 +'), { budget });
    if (r.status === 'exceeded') {
      check(r.reason === 'budget', `budget=${budget}: reason should be budget`);
      check(r.lowerBound <= 1 && 1 <= r.upperBound, `budget=${budget}: bounds ${r.lowerBound}..${r.upperBound} must contain 1`);
      check(r.lowerBound <= r.upperBound, `budget=${budget}: lower <= upper`);
    } else {
      check(r.cost === 1, `budget=${budget}: cost 1 expected`);
    }
  }
  const r0 = repair(lex('0 +'), { budget: 0 });
  check(r0.status === 'exceeded' && r0.lowerBound === 0 && r0.upperBound === 3, 'budget=0: bounds 0..3');
}

{
  const r = repair(lex('0 +'), { maxCost: 0 });
  check(r.status === 'exceeded' && r.reason === 'max-cost', 'maxCost=0: exceeded');
  if (r.status === 'exceeded') {
    check(r.lowerBound === 1 && r.upperBound === 3, `maxCost=0: bounds 1..3, got ${r.lowerBound}..${r.upperBound}`);
  }
}

{
  // Determinism.
  const a = repair(lex('(0+,]'));
  const b = repair(lex('(0+,]'));
  check(
    a.status === 'ok' && b.status === 'ok' && compareEditSeqs(a.edits, b.edits) === 0,
    'determinism',
  );
}

console.log(`\n${checks} checks, ${failures} failures`);
if (failures > 0) process.exit(1);
console.log('ALL OK');
