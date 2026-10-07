import * as assert from 'assert';
import { TextEdit } from '../src/types';

export interface TestCase {
  name: string;
  fn: () => void;
}

const suites: { name: string; cases: TestCase[] }[] = [];
let current: { name: string; cases: TestCase[] } | undefined;

export function describe(name: string, body: () => void): void {
  current = { name, cases: [] };
  suites.push(current);
  body();
  current = undefined;
}

export function it(name: string, fn: () => void): void {
  if (!current) throw new Error('it() outside describe()');
  current.cases.push({ name, fn });
}

export function runAll(): void {
  let failed = 0;
  let passed = 0;
  for (const suite of suites) {
    console.log(`\n${suite.name}`);
    for (const c of suite.cases) {
      try {
        c.fn();
        passed += 1;
        console.log(`  ok - ${c.name}`);
      } catch (err) {
        failed += 1;
        console.log(`  FAIL - ${c.name}`);
        console.log(`    ${err instanceof Error ? err.message : String(err)}`);
      }
    }
  }
  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
}

export { assert };

/**
 * Naive concatenation model: the expected new text is nothing more than
 * unchanged slices and replacement texts spliced together in order.
 * This is the independent reference the library output is checked against.
 */
export function naiveConcat(text: string, edits: TextEdit[]): string {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  let out = '';
  let cursor = 0;
  const pieces: string[] = [];
  for (const e of sorted) {
    pieces.push(text.slice(cursor, e.start), e.newText);
    cursor = e.end;
  }
  pieces.push(text.slice(cursor));
  out = pieces.join('');
  return out;
}

/** Offsets where each replacement lands in the naive model output. */
export function naiveReplacementOffsets(text: string, edits: TextEdit[]): { start: number; end: number }[] {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  const offsets: { start: number; end: number }[] = [];
  let delta = 0;
  for (const e of sorted) {
    const start = e.start + delta;
    offsets.push({ start, end: start + e.newText.length });
    delta += e.newText.length - (e.end - e.start);
  }
  return offsets;
}
