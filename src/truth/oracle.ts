/**
 * Independent small-tree truth interpreter.
 *
 * It works on a deliberately flat representation (one opinion per path) and
 * resolves each path with an obvious priority scan, so it can cross-check the
 * tree engine without sharing its traversal implementation.
 */

export type FlatState =
  | { t: 'value'; v: string | number | boolean }
  | { t: 'null' }
  | { t: 'tomb' };

export interface FlatOpinion {
  branch: string;
  priority: number;
  /** Missing keys are simply absent from this record. */
  states: Record<string, FlatState>;
}

export interface FlatRetraction {
  path: string;
  branch?: string;
}

export interface TruthInput {
  paths: string[];
  opinions: FlatOpinion[];
  retractions?: FlatRetraction[];
}

export type TruthOutcomeKind = 'value' | 'null' | 'deleted' | 'missing';

export interface TruthOutcome {
  path: string;
  kind: TruthOutcomeKind;
  value?: string | number | boolean;
  sourceBranch?: string;
  exposed: boolean;
  deletedBranch?: string;
}

/** Naive ground-truth resolution for every flat path. */
export function truthOracle(input: TruthInput): Record<string, TruthOutcome> {
  const result: Record<string, TruthOutcome> = {};

  for (const path of input.paths) {
    let candidates = input.opinions
      .map((opinion) => ({ opinion, state: opinion.states[path] }))
      .filter((entry): entry is { opinion: FlatOpinion; state: FlatState } => entry.state !== undefined)
      .sort((a, b) => b.opinion.priority - a.opinion.priority);

    let deletedBranch: string | undefined;
    let exposed = false;

    for (const retraction of input.retractions ?? []) {
      if (retraction.path !== path) continue;
      let index = -1;
      if (retraction.branch) {
        index = candidates.findIndex((c) => c.opinion.branch === retraction.branch);
      } else {
        index = 0;
      }
      if (index >= 0) {
        deletedBranch = candidates[index].opinion.branch;
        candidates = candidates.slice(index + 1);
        exposed = candidates.length > 0;
      }
    }

    const top = candidates[0];
    if (!top) {
      result[path] = { path, kind: deletedBranch ? 'deleted' : 'missing', exposed, ...(deletedBranch ? { deletedBranch } : {}) };
      continue;
    }

    if (top.state.t === 'tomb') {
      result[path] = { path, kind: 'deleted', exposed: false, sourceBranch: top.opinion.branch };
    } else if (top.state.t === 'null') {
      result[path] = { path, kind: 'null', exposed, sourceBranch: top.opinion.branch, ...(deletedBranch ? { deletedBranch } : {}) };
    } else {
      result[path] = {
        path,
        kind: 'value',
        value: top.state.v,
        exposed,
        sourceBranch: top.opinion.branch,
        ...(deletedBranch ? { deletedBranch } : {}),
      };
    }
  }

  return result;
}

const STATE_CHOICES: (FlatState | undefined)[] = [
  undefined, // missing
  { t: 'value', v: 'x' },
  { t: 'null' },
  { t: 'tomb' },
];

export interface ExhaustiveCase {
  index: number;
  opinions: FlatOpinion[];
  truth: Record<string, TruthOutcome>;
}

/**
 * Enumerate every combination of the four states across `branchCount`
 * single-key branches. Gives 4^branchCount deterministic truth cases.
 */
export function exhaustiveStateTruth(branchCount = 3): ExhaustiveCase[] {
  const total = 4 ** branchCount;
  const cases: ExhaustiveCase[] = [];
  for (let index = 0; index < total; index += 1) {
    let code = index;
    const states: (FlatState | undefined)[] = [];
    for (let b = 0; b < branchCount; b += 1) {
      states.push(STATE_CHOICES[code % 4]);
      code = Math.floor(code / 4);
    }
    const opinions: FlatOpinion[] = states.map((state, b) => ({
      branch: `b${b}`,
      priority: (b + 1) * 10,
      states: state ? { k: state } : {},
    }));
    cases.push({ index, opinions, truth: truthOracle({ paths: ['k'], opinions }) });
  }
  return cases;
}
