import {
  aggregateStats,
  exploreComponent,
  type BudgetTracker,
  type ComponentSafe,
  type ComponentUnsafe,
} from "./explore.ts";
import { buildComponents } from "./components.ts";
import { invalidResult, normalizeGraph } from "./model.ts";
import {
  DEFAULT_BUDGET,
  type AnalysisResult,
  type AnalyzeOptions,
  type Budget,
  type EventId,
  type OperationGraph,
  type SafetyProof,
  type ViolationWitness,
} from "./types.ts";

function resolveBudget(options: AnalyzeOptions): Required<Budget> {
  const nested = options.budget;
  const maxStates =
    options.maxStates ?? nested?.maxStates ?? DEFAULT_BUDGET.maxStates;
  const maxTransitions =
    options.maxTransitions ??
    nested?.maxTransitions ??
    DEFAULT_BUDGET.maxTransitions;
  const maxDepth =
    options.maxDepth ?? nested?.maxDepth ?? DEFAULT_BUDGET.maxDepth;
  if (!Number.isInteger(maxStates) || maxStates < 0) {
    throw new Error("maxStates must be a non-negative integer");
  }
  if (!Number.isInteger(maxTransitions) || maxTransitions < 0) {
    throw new Error("maxTransitions must be a non-negative integer");
  }
  if (!Number.isInteger(maxDepth) || maxDepth < 0) {
    throw new Error("maxDepth must be a non-negative integer");
  }
  return { maxStates, maxTransitions, maxDepth };
}

function comparePrefixes(
  left: readonly EventId[],
  right: readonly EventId[],
  compareEventIds: (left: EventId, right: EventId) => number,
): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const order = compareEventIds(
      left[index] as EventId,
      right[index] as EventId,
    );
    if (order !== 0) {
      return order;
    }
  }
  return left.length - right.length;
}

/**
 * Verify every execution prefix of the operation partial order.
 *
 * Unsafe results always contain a shortest violating prefix and, among equal
 * lengths, the lexicographically smallest event-id sequence.  A safe result
 * carries a state-closure certificate that can be checked independently with
 * `verifySafetyProof`.
 */
export function analyze(
  graph: OperationGraph,
  options: AnalyzeOptions = {},
): AnalysisResult {
  try {
    const normalized = normalizeGraph(graph, options);
    const components = buildComponents(normalized);
    const budget = resolveBudget(options);
    const tracker: BudgetTracker = {
      maxStates: budget.maxStates,
      maxTransitions: budget.maxTransitions,
      maxDepth: budget.maxDepth,
      states: 0,
      transitions: 0,
    };

    const safeProofs: ComponentSafe[] = [];
    let best: ComponentUnsafe | undefined;
    let budgetFailure: string | undefined;

    for (const component of components) {
      if (tracker.states >= tracker.maxStates) {
        budgetFailure = `state limit ${tracker.maxStates} reached`;
        break;
      }
      const outcome = exploreComponent(normalized, component, tracker);
      if (outcome.status === "budget") {
        budgetFailure = outcome.reason;
        break;
      }
      if (outcome.status === "unsafe") {
        if (
          best === undefined ||
          comparePrefixes(
            outcome.prefix,
            best.prefix,
            normalized.compareEventIds,
          ) < 0
        ) {
          best = outcome;
        }
        continue;
      }
      safeProofs.push(outcome);
    }

    const stats = aggregateStats(
      components.length,
      {
        states: tracker.states,
        transitions: tracker.transitions,
        enabledOccurrences: tracker.transitions,
      },
      normalized.events.length,
    );

    if (budgetFailure !== undefined) {
      return {
        status: "unknown",
        reason: "budget",
        budget,
        explored: stats,
        partial: best === undefined ? undefined : { violation: best.witness },
      };
    }

    if (best !== undefined) {
      return {
        status: "unsafe",
        prefix: best.prefix,
        violation: best.violation,
        witness: best.witness,
        explored: stats,
      };
    }

    const proofComponents = components.map((component) => {
      const found = safeProofs.find(
        (entry) => entry.proof.componentId === component.id,
      );
      if (found === undefined) {
        throw new Error(`internal error: missing proof for component ${component.id}`);
      }
      return found.proof;
    });
    const proof: SafetyProof = {
      kind: "state-closure",
      version: 1,
      graphDigest: normalized.digest,
      eventPartition: components.map((component) => [...component.eventIds]),
      components: proofComponents,
      totals: {
        components: components.length,
        states: tracker.states,
        transitions: tracker.transitions,
        enabledOccurrences: tracker.transitions,
        events: normalized.events.length,
      },
    };
    return { status: "safe", proof, explored: stats };
  } catch (error) {
    return invalidResult(error);
  }
}

export const analyzeGraph = analyze;
export const analyzeProtectionGraph = analyze;
export const verify = analyze;
export const runAnalysis = analyze;

export function violationWitnessOf(result: AnalysisResult): ViolationWitness | undefined {
  if (result.status === "unsafe") {
    return result.witness;
  }
  if (result.status === "unknown") {
    return result.partial?.violation;
  }
  return undefined;
}
