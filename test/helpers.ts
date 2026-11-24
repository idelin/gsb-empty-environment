import {
  applyEvent,
  buildInitialState,
  normalizeGraph,
  type AnalysisResult,
  type AnalyzeOptions,
  type EventId,
  type OperationGraph,
  type ViolationDetail,
} from "../src/index.ts";

export interface BruteAnswer {
  readonly prefix?: EventId[];
  readonly violation?: ViolationDetail;
}

function compare(left: readonly EventId[], right: readonly EventId[]): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    if (left[index] !== right[index]) {
      return (left[index] as string) < (right[index] as string) ? -1 : 1;
    }
  }
  return left.length - right.length;
}

/**
 * Deliberately naive permutation checker for tiny graphs.  It explores every
 * linear extension and every prefix, so it is a useful oracle for `analyze`.
 */
export function bruteForce(
  graph: OperationGraph,
  options: AnalyzeOptions = {},
): BruteAnswer {
  const normalized = normalizeGraph(graph, options);
  let bestPrefix: EventId[] | undefined;
  let bestViolation: ViolationDetail | undefined;

  const visit = (
    state: ReturnType<typeof buildInitialState>,
    prefix: EventId[],
  ): void => {
    for (const event of normalized.events) {
      if (state.executed.has(event.id)) {
        continue;
      }
      let enabled = true;
      for (const dependency of event.before) {
        if (!state.executed.has(dependency)) {
          enabled = false;
          break;
        }
      }
      if (!enabled) {
        continue;
      }
      const result = applyEvent(state, event, normalized);
      const nextPrefix = [...prefix, event.id];
      if (result.violation !== undefined) {
        if (
          bestPrefix === undefined ||
          nextPrefix.length < bestPrefix.length ||
          (nextPrefix.length === bestPrefix.length &&
            compare(nextPrefix, bestPrefix) < 0)
        ) {
          bestPrefix = nextPrefix;
          bestViolation = result.violation;
        }
        continue;
      }
      visit(result.state, nextPrefix);
    }
  };

  visit(buildInitialState(normalized), []);
  return { prefix: bestPrefix, violation: bestViolation };
}

export function assertUnsafe(
  result: AnalysisResult,
  expected: readonly EventId[],
): void {
  if (result.status !== "unsafe") {
    throw new Error(`expected unsafe, got ${result.status}`);
  }
  const actual = JSON.stringify(result.prefix);
  const wanted = JSON.stringify(expected);
  if (actual !== wanted) {
    throw new Error(`expected prefix ${wanted}, got ${actual}`);
  }
}
