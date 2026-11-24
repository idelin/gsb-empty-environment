import type { ComponentSpec } from "./components.ts";
import {
  applyEvent,
  buildInitialState,
  replayPrefix,
  stateKey,
  type NormalizedEvent,
  type NormalizedGraph,
  type RuntimeState,
} from "./model.ts";
import { serializeState } from "./serialize.ts";
import type {
  ClosureTransition,
  ComponentSafetyProof,
  EventId,
  ExplorationStats,
  SerializedState,
  ViolationDetail,
  ViolationWitness,
} from "./types.ts";

export interface BudgetTracker {
  readonly maxStates: number;
  readonly maxTransitions: number;
  readonly maxDepth: number;
  states: number;
  transitions: number;
}

export interface ComponentSafe {
  readonly status: "safe";
  readonly proof: ComponentSafetyProof;
  readonly states: number;
  readonly transitions: number;
  readonly enabledOccurrences: number;
}

export interface ComponentUnsafe {
  readonly status: "unsafe";
  readonly prefix: readonly EventId[];
  readonly violation: ViolationDetail;
  readonly witness: ViolationWitness;
}

export interface ComponentBudget {
  readonly status: "budget";
  readonly reason: string;
}

export type ComponentOutcome =
  | ComponentSafe
  | ComponentUnsafe
  | ComponentBudget;

function buildWitness(
  graph: NormalizedGraph,
  prefix: readonly EventId[],
  violation: ViolationDetail,
): ViolationWitness {
  const replay = replayPrefix(graph, prefix);
  return {
    prefix: [...prefix],
    violation,
    steps: replay.steps,
  };
}

/**
 * Exhaustively explore one independent component.  BFS order gives both
 * guarantees required by the specification:
 *   - the first violation is at a shortest prefix,
 *   - among equal lengths, the first sequence is lexicographically smallest.
 * State deduplication keeps the first (therefore lexicographically smallest)
 * path to each state.
 */
export function exploreComponent(
  graph: NormalizedGraph,
  component: ComponentSpec,
  tracker: BudgetTracker,
): ComponentOutcome {
  if (tracker.states >= tracker.maxStates) {
    return {
      status: "budget",
      reason: `state limit ${tracker.maxStates} reached`,
    };
  }
  const initial = buildInitialState(graph);
  const states: RuntimeState[] = [initial];
  const paths: EventId[][] = [[]];
  const ids = new Map<string, number>([[stateKey(initial), 0]]);
  const transitions: ClosureTransition[] = [];
  const checked = new Set<EventId>();
  let enabledOccurrences = 0;
  let queueIndex = 0;
  tracker.states += 1;

  while (queueIndex < states.length) {
    const stateId = queueIndex;
    queueIndex += 1;
    const state = states[stateId] as RuntimeState;
    const prefix = paths[stateId] as EventId[];
    if (prefix.length >= tracker.maxDepth) {
      let hasEnabled = false;
      for (const event of component.events) {
        if (state.executed.has(event.id)) {
          continue;
        }
        if (isEnabled(state, event)) {
          hasEnabled = true;
          break;
        }
      }
      if (hasEnabled) {
        return {
          status: "budget",
          reason: `depth limit ${tracker.maxDepth} reached`,
        };
      }
    }

    for (const event of component.events) {
      if (state.executed.has(event.id) || !isEnabled(state, event)) {
        continue;
      }
      if (tracker.transitions >= tracker.maxTransitions) {
        return {
          status: "budget",
          reason: `transition limit ${tracker.maxTransitions} reached`,
        };
      }
      const result = applyEvent(state, event, graph);
      checked.add(event.id);
      enabledOccurrences += 1;
      tracker.transitions += 1;

      if (result.violation !== undefined) {
        const violationPrefix = [...prefix, event.id];
        return {
          status: "unsafe",
          prefix: violationPrefix,
          violation: result.violation,
          witness: buildWitness(graph, violationPrefix, result.violation),
        };
      }

      const key = stateKey(result.state);
      let successor = ids.get(key);
      if (successor === undefined) {
        if (tracker.states >= tracker.maxStates) {
          return {
            status: "budget",
            reason: `state limit ${tracker.maxStates} reached`,
          };
        }
        successor = states.length;
        ids.set(key, successor);
        states.push(result.state);
        paths.push([...prefix, event.id]);
        tracker.states += 1;
      }
      transitions.push({
        from: stateId,
        event: event.id,
        to: successor,
        outcome: result.outcome,
        rejection: result.rejection?.code,
      });
    }
  }

  const serialized: SerializedState[] = states.map((state, id) =>
    serializeState(state, id),
  );
  return {
    status: "safe",
    proof: {
      componentId: component.id,
      events: [...component.eventIds],
      initialState: serialized[0] as SerializedState,
      states: serialized,
      transitions,
      checkedEvents: [...checked].sort(graph.compareEventIds),
      enabledOccurrences,
    },
    states: states.length,
    transitions: transitions.length,
    enabledOccurrences,
  };
}

function isEnabled(state: RuntimeState, event: NormalizedEvent): boolean {
  for (const dependency of event.before) {
    if (!state.executed.has(dependency)) {
      return false;
    }
  }
  return true;
}

export function aggregateStats(
  components: number,
  totals: { states: number; transitions: number; enabledOccurrences: number },
  events: number,
): ExplorationStats {
  return {
    components,
    states: totals.states,
    transitions: totals.transitions,
    enabledOccurrences: totals.enabledOccurrences,
    events,
  };
}
