import { createHash } from 'node:crypto';
import type {
  AltWitness,
  CertifiedPair,
  Grammar,
  GrammarAlt,
  InclusionCertificate,
  InclusionVerdict,
  WideningEvidence,
} from './types.ts';
import { assemblyRefSubset } from './version.ts';

export const RULE_VERSION = 'type-admission/rules/v1';

export class BudgetExceeded extends Error {
  readonly budget: number;
  constructor(budget: number) {
    super(`inclusion check exceeded pair budget ${budget}`);
    this.name = 'BudgetExceeded';
    this.budget = budget;
  }
}

export function canonicalHash(value: unknown): string {
  return createHash('sha256').update(canonicalStringify(value)).digest('base64url');
}

export function canonicalStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalStringify).join(',')}]`;
  const entries = Object.entries(value as Record<string, unknown>)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${canonicalStringify(v)}`).join(',')}}`;
}

type PairState = 'white' | 'gray' | 'black';

interface PairStateRecord {
  inner: string;
  outer: string;
  state: PairState;
  matches: AltWitness[];
}

interface AltProgress {
  innerAltIndex: number;
  outerAltIndex: number;
  argIndex: number;
  argPairs: { newNt: string; oldNt: string }[];
}

interface PairFrame {
  record: PairStateRecord;
  alt: AltProgress;
  path: number[];
}

function pairKey(inner: string, outer: string): string {
  return `${inner}\u0000${outer}`;
}

function headMatches(inner: GrammarAlt, outer: GrammarAlt): boolean {
  if (inner.typeName !== outer.typeName) return false;
  if ((inner.arrayRank ?? 0) !== (outer.arrayRank ?? 0)) return false;
  if ((inner.args ?? []).length !== (outer.args ?? []).length) return false;
  if ((inner.element ?? '') !== (outer.element ?? '')) return false;
  return assemblyRefSubset(inner.assembly, outer.assembly);
}

/**
 * Decide whether L(inner) ⊆ L(outer) over guarded regular tree grammars.
 *
 * Coinductive simulation: a pair encountered while gray (on the active
 * stack) is accepted as a hypothesis. Guardedness — enforced while grammars
 * are compiled — makes this sound, because every recursive use lies behind
 * at least one constructor and the hypothesis can only be observed on
 * strictly larger finite unfoldings.
 */
export function checkInclusion(
  inner: Grammar,
  outer: Grammar,
  budget: number,
  now: Date = new Date(),
): InclusionCertificate {
  const innerByNt = new Map(inner.productions.map((p) => [p.nt, p]));
  const outerByNt = new Map(outer.productions.map((p) => [p.nt, p]));
  const records = new Map<string, PairStateRecord>();
  let pairsUsed = 0;
  let counterexample: WideningEvidence | undefined;

  const acquire = (innerNt: string, outerNt: string): PairStateRecord => {
    const key = pairKey(innerNt, outerNt);
    const existing = records.get(key);
    if (existing !== undefined) return existing;
    if (pairsUsed >= budget) throw new BudgetExceeded(budget);
    pairsUsed += 1;
    const record: PairStateRecord = { inner: innerNt, outer: outerNt, state: 'gray', matches: [] };
    records.set(key, record);
    return record;
  };

  const stack: PairFrame[] = [];
  const startRecord = acquire(inner.start, outer.start);
  stack.push({
    record: startRecord,
    alt: { innerAltIndex: 0, outerAltIndex: -1, argIndex: 0, argPairs: [] },
    path: [],
  });

  let failed = false;

  const fail = (path: number[], expected: string, found: string): void => {
    failed = true;
    counterexample = { path, expected, found };
  };

  while (stack.length > 0 && !failed) {
    const frame = stack[stack.length - 1];
    const { record } = frame;
    if (record.state === 'black') {
      stack.pop();
      continue;
    }
    const innerProd = innerByNt.get(record.inner);
    const outerProd = outerByNt.get(record.outer);
    if (innerProd === undefined || outerProd === undefined) {
      fail(frame.path, record.outer, record.inner);
      break;
    }

    if (frame.alt.innerAltIndex >= innerProd.alts.length) {
      record.state = 'black';
      stack.pop();
      continue;
    }

    const innerAlt = innerProd.alts[frame.alt.innerAltIndex];
    if (frame.alt.outerAltIndex < 0) {
      const matchIndex = outerProd.alts.findIndex((candidate) => headMatches(innerAlt, candidate));
      if (matchIndex < 0) {
        fail(
          frame.path,
          describeHead(outerProd.alts[0]),
          describeHead(innerAlt),
        );
        break;
      }
      frame.alt.outerAltIndex = matchIndex;
    }
    const outerAlt = outerProd.alts[frame.alt.outerAltIndex];
    const innerArgs = innerAlt.args ?? [];
    const outerArgs = outerAlt.args ?? [];

    if (frame.alt.argIndex >= innerArgs.length) {
      record.matches.push({
        newAltIndex: frame.alt.innerAltIndex,
        oldAltIndex: frame.alt.outerAltIndex,
        argPairs: frame.alt.argPairs,
      });
      frame.alt = {
        innerAltIndex: frame.alt.innerAltIndex + 1,
        outerAltIndex: -1,
        argIndex: 0,
        argPairs: [],
      };
      continue;
    }

    const position = frame.alt.argIndex;
    const innerArg = innerArgs[position];
    const outerArg = outerArgs[position];
    const childPath = [...frame.path, frame.alt.innerAltIndex, position];
    frame.alt.argIndex += 1;

    if (innerArg.variance !== outerArg.variance) {
      fail(childPath, `variance ${outerArg.variance}`, `variance ${innerArg.variance}`);
      break;
    }

    const orientations: [string, string][] =
      innerArg.variance === 'contravariant'
        ? [[outerArg.nt, innerArg.nt]]
        : innerArg.variance === 'invariant'
          ? [
              [innerArg.nt, outerArg.nt],
              [outerArg.nt, innerArg.nt],
            ]
          : [[innerArg.nt, outerArg.nt]];

    for (const [childInner, childOuter] of orientations) {
      const childRecord = acquire(childInner, childOuter);
      frame.alt.argPairs.push({ newNt: childInner, oldNt: childOuter });
      if (childRecord.state === 'white') {
        childRecord.state = 'gray';
      }
      if (childRecord.state === 'gray' && childRecord !== record) {
        stack.push({
          record: childRecord,
          alt: { innerAltIndex: 0, outerAltIndex: -1, argIndex: 0, argPairs: [] },
          path: childPath,
        });
      }
    }
  }

  const verdict: InclusionVerdict = failed ? 'widened' : 'subset';
  const proof: CertifiedPair[] = [...records.values()].map((record) => ({
    newNt: record.inner,
    oldNt: record.outer,
    matches: record.matches,
  }));

  return {
    format: 'type-admission/inclusion-cert/v1',
    ruleVersion: RULE_VERSION,
    verdict,
    budget,
    pairsUsed,
    oldGrammarHash: canonicalHash(outer),
    newGrammarHash: canonicalHash(inner),
    proof,
    counterexample,
    issuedAt: now.toISOString(),
  };
}

function describeHead(alt: GrammarAlt | undefined): string {
  if (alt === undefined) return '<no alternative>';
  const rank = alt.arrayRank !== undefined && alt.arrayRank > 0 ? `[rank:${alt.arrayRank}]` : '';
  return `${alt.assembly.publicKey}/${alt.typeName}${rank}`;
}

/**
 * Budget-safe wrapper. Exceeding the pair budget is never published as
 * compatible: the verdict becomes `unknown` and the proof is withheld.
 */
export function certifyInclusion(
  inner: Grammar,
  outer: Grammar,
  budget: number,
  now: Date = new Date(),
): InclusionCertificate {
  try {
    return checkInclusion(inner, outer, budget, now);
  } catch (error) {
    if (!(error instanceof BudgetExceeded)) throw error;
    return {
      format: 'type-admission/inclusion-cert/v1',
      ruleVersion: RULE_VERSION,
      verdict: 'unknown',
      budget,
      pairsUsed: budget,
      oldGrammarHash: canonicalHash(outer),
      newGrammarHash: canonicalHash(inner),
      proof: [],
      issuedAt: now.toISOString(),
    };
  }
}
