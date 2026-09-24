import type {
  Candidate,
  ContextSummary,
  ExcludedCandidate,
  Relocation,
} from './types.ts';

/**
 * Normalize CRLF (and lone CR) line endings to LF while remembering, for
 * every offset in the normalized string, the corresponding UTF-16 offset
 * in the original string.
 */
export function normalizeLineEndingsWithMap(text: string): {
  norm: string;
  toOrig: number[];
} {
  let norm = '';
  const toOrig: number[] = [];
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === '\r') {
      norm += '\n';
      toOrig.push(i);
      if (text[i + 1] === '\n') i++; // collapse CRLF into one LF
    } else {
      norm += ch;
      toOrig.push(i);
    }
  }
  toOrig.push(text.length);
  return { norm, toOrig };
}

export function normalizeLineEndings(text: string): string {
  return text.replace(/\r\n/g, '\n').replace(/\r/g, '\n');
}

function commonSuffixLength(a: string, b: string): number {
  let n = 0;
  while (n < a.length && n < b.length && a[a.length - 1 - n] === b[b.length - 1 - n]) {
    n++;
  }
  return n;
}

function commonPrefixLength(a: string, b: string): number {
  let n = 0;
  while (n < a.length && n < b.length && a[n] === b[n]) {
    n++;
  }
  return n;
}

/**
 * Fallback relocation when no identity evidence is available (e.g. the
 * diagnostic's version lineage was reclaimed, or the target version is on
 * a branch that never contained the marker).
 *
 * Every occurrence of the target text becomes a candidate (generation
 * evidence). Candidates whose surrounding context matches worse than the
 * best match are excluded with explicit reasons. A unique best candidate
 * yields `certain` with basis `text-similarity`; a tie yields `ambiguous`
 * — duplicates are never silently resolved by picking the first match.
 */
export function similarityRelocate(
  context: ContextSummary,
  text: string,
  note: string,
): Relocation {
  const { norm, toOrig } = normalizeLineEndingsWithMap(text);
  const target = normalizeLineEndings(context.target);
  const evidence: string[] = [note];
  if (target.length === 0) {
    return {
      status: 'invalid',
      reason: 'diagnostic target text is empty; text search is impossible',
      evidence,
    };
  }

  const occurrences: number[] = [];
  let idx = norm.indexOf(target);
  while (idx !== -1) {
    occurrences.push(idx);
    idx = norm.indexOf(target, idx + 1);
  }
  if (occurrences.length === 0) {
    evidence.push(`target text ${JSON.stringify(context.target)} not found in target version`);
    return { status: 'invalid', reason: 'target text not found', evidence };
  }

  const prefixN = normalizeLineEndings(context.prefix);
  const suffixN = normalizeLineEndings(context.suffix);
  const candidates: Candidate[] = occurrences.map((normIndex, i) => {
    const before = norm.slice(0, normIndex);
    const after = norm.slice(normIndex + target.length);
    const pre = commonSuffixLength(before, prefixN);
    const suf = commonPrefixLength(after, suffixN);
    const range = {
      start: toOrig[normIndex],
      end: toOrig[normIndex + target.length],
    };
    return {
      range,
      contextScore: pre + suf,
      evidence:
        `occurrence #${i + 1} of target text at UTF-16 [${range.start}, ${range.end}) ` +
        `(line-ending-insensitive search); context match: ${pre} prefix + ${suf} suffix unit(s)`,
    };
  });
  evidence.push(`found ${candidates.length} occurrence(s) of the target text`);

  const maxScore = Math.max(...candidates.map((c) => c.contextScore));
  const best = candidates.filter((c) => c.contextScore === maxScore);
  const excluded: ExcludedCandidate[] = candidates
    .filter((c) => c.contextScore < maxScore)
    .map((c) => ({
      range: c.range,
      reason: `context score ${c.contextScore} is below best score ${maxScore}`,
    }));

  if (best.length === 1) {
    evidence.push(
      `unique best candidate at [${best[0].range.start}, ${best[0].range.end}) ` +
        `with context score ${maxScore}`,
    );
    return {
      status: 'certain',
      basis: 'text-similarity',
      range: best[0].range,
      evidence,
      excluded,
    };
  }

  evidence.push(
    `${best.length} candidates tie with context score ${maxScore}; refusing to pick one arbitrarily`,
  );
  return { status: 'ambiguous', candidates: best, excluded, evidence };
}
