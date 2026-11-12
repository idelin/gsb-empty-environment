import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { globCovers, globMatches } from '../src/match-pattern.ts';

const alphabet = ['a', 'b', '/'];

function allStrings(maxLen: number): string[] {
  const out: string[] = [''];
  let frontier = [''];
  for (let len = 0; len < maxLen; len++) {
    const next: string[] = [];
    for (const str of frontier) {
      for (const ch of alphabet) {
        next.push(str + ch);
      }
    }
    out.push(...next);
    frontier = next;
  }
  return out;
}

function allGlobs(maxLen: number): string[] {
  const tokens = [...alphabet, '*'];
  const out: string[] = [''];
  let frontier = [''];
  for (let len = 0; len < maxLen; len++) {
    const next: string[] = [];
    for (const prefix of frontier) {
      for (const token of tokens) {
        next.push(prefix + token);
      }
    }
    out.push(...next);
    frontier = next;
  }
  return out.filter((glob) => glob.length > 0);
}

describe('globCovers exhaustive language check', () => {
  it('never reports non-coverage without a concrete witness string', () => {
    // Soundness direction, which holds on any finite universe: whenever
    // globCovers claims the child escapes the parent, some enumerated string
    // must actually be accepted by the child and rejected by the parent.
    // This catches every false-negative bug in the inclusion automaton.
    const universe = allStrings(8);
    const globs = allGlobs(4);
    assert.ok(universe.length >= 9000);
    assert.ok(globs.length >= 120);
    for (const parent of globs) {
      for (const child of globs) {
        if (!globCovers(parent, child)) {
          const witness = universe.find(
            (str) => globMatches(child, str) && !globMatches(parent, str),
          );
          assert.ok(
            witness !== undefined,
            `globCovers(${JSON.stringify(parent)}, ${JSON.stringify(child)}) reported non-coverage without a witness`,
          );
        }
      }
    }
  });

  it('reports coverage whenever the brute-force universe is fully contained', () => {
    // Completeness direction on a universe sized beyond the globs so every
    // accepting string is enumerated; the brute-force inclusion answer is
    // then exact and globCovers must agree.
    const universe = allStrings(6);
    const globs = allGlobs(3);
    for (const parent of globs) {
      for (const child of globs) {
        const bruteForce = universe.every((str) => !globMatches(child, str) || globMatches(parent, str));
        if (bruteForce) {
          assert.equal(
            globCovers(parent, child),
            true,
            `globCovers(${JSON.stringify(parent)}, ${JSON.stringify(child)})`,
          );
        }
      }
    }
  });
});
