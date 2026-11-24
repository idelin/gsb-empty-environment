import { buildComponents } from "./components.ts";
import {
  GraphError,
  applyEvent,
  buildInitialState,
  normalizeGraph,
  stateKey,
  type NormalizedGraph,
  type RuntimeState,
} from "./model.ts";
import { deserializeState } from "./serialize.ts";
import type {
  AnalyzeOptions,
  ClosureTransition,
  ComponentSafetyProof,
  EventId,
  ProofCheckResult,
  SafetyProof,
  SerializedState,
} from "./types.ts";

function sortedIds(ids: readonly EventId[]): EventId[] {
  return [...ids].sort();
}

function sameSet(left: readonly EventId[], right: readonly EventId[]): boolean {
  if (left.length !== right.length) {
    return false;
  }
  const leftSorted = sortedIds(left);
  const rightSorted = sortedIds(right);
  return leftSorted.every((value, index) => value === rightSorted[index]);
}

function transitionKey(from: number, event: EventId): string {
  return `${from}\u0000${event}`;
}

function checkComponent(
  graph: NormalizedGraph,
  expectedEventIds: readonly EventId[],
  proof: ComponentSafetyProof,
  addError: (message: string) => void,
): { states: number; transitions: number; enabledEvents: number } {
  if (proof === null || typeof proof !== "object") {
    addError("component proof is not an object");
    return { states: 0, transitions: 0, enabledEvents: 0 };
  }
  if (!Array.isArray(proof.events)) {
    addError(`component ${proof.componentId} events is not an array`);
    return { states: 0, transitions: 0, enabledEvents: 0 };
  }
  if (!sameSet(proof.events, expectedEventIds)) {
    addError(
      `component ${proof.componentId} event set does not match the graph partition`,
    );
  }
  if (!Array.isArray(proof.states) || proof.states.length === 0) {
    addError(`component ${proof.componentId} has no states`);
    return { states: 0, transitions: 0, enabledEvents: 0 };
  }
  if (!Array.isArray(proof.transitions)) {
    addError(`component ${proof.componentId} transitions is not an array`);
    return { states: 0, transitions: 0, enabledEvents: 0 };
  }

  const localStates: RuntimeState[] = [];
  for (let index = 0; index < proof.states.length; index += 1) {
    const serialized = proof.states[index] as SerializedState;
    if (
      serialized === null ||
      typeof serialized !== "object"
    ) {
      addError(
        `component ${proof.componentId} state ${index} is not an object`,
      );
      localStates.push(buildInitialState(graph));
      continue;
    }
    if (serialized.id !== index) {
      addError(
        `component ${proof.componentId} state ${index} has id ${serialized.id}`,
      );
    }
    try {
      localStates.push(deserializeState(serialized));
    } catch (error) {
      addError(
        `component ${proof.componentId} state ${index} is malformed: ${String(error)}`,
      );
      localStates.push(buildInitialState(graph));
    }
  }

  const expectedInitial = buildInitialState(graph);
  if (
    proof.states.length === 0 ||
    stateKey(localStates[0] as RuntimeState) !== stateKey(expectedInitial) ||
    proof.initialState?.id !== 0
  ) {
    addError(`component ${proof.componentId} initial state does not match`);
  } else {
    try {
      const initial = deserializeState(proof.initialState);
      if (stateKey(initial) !== stateKey(expectedInitial)) {
        addError(`component ${proof.componentId} initial state does not match`);
      }
    } catch (error) {
      addError(
        `component ${proof.componentId} initial state is malformed: ${String(error)}`,
      );
    }
  }

  const componentEvents = new Set(proof.events);
  const byId = new Map<string, ClosureTransition>();
  const outgoing = new Map<number, ClosureTransition[]>();
  for (const transition of proof.transitions) {
    if (transition === null || typeof transition !== "object") {
      addError(`component ${proof.componentId} has a malformed transition`);
      continue;
    }
    const key = transitionKey(transition.from, transition.event);
    if (byId.has(key)) {
      addError(
        `component ${proof.componentId} has duplicate transition ${transition.from}/${transition.event}`,
      );
      continue;
    }
    byId.set(key, transition);
    const list = outgoing.get(transition.from) ?? [];
    list.push(transition);
    outgoing.set(transition.from, list);
    const from = localStates[transition.from];
    const to = localStates[transition.to];
    const event = graph.eventById.get(transition.event);
    if (from === undefined) {
      addError(
        `component ${proof.componentId} transition references state ${transition.from}`,
      );
      continue;
    }
    if (to === undefined) {
      addError(
        `component ${proof.componentId} transition references state ${transition.to}`,
      );
      continue;
    }
    if (event === undefined) {
      addError(
        `component ${proof.componentId} references unknown event ${transition.event}`,
      );
      continue;
    }
    if (!componentEvents.has(event.id)) {
      addError(
        `component ${proof.componentId} uses event from another component`,
      );
      continue;
    }
    if (from.executed.has(event.id)) {
      addError(
        `component ${proof.componentId} repeats event ${event.id}`,
      );
      continue;
    }
    for (const dependency of event.before) {
      if (!from.executed.has(dependency)) {
        addError(
          `component ${proof.componentId} executes ${event.id} before ${dependency}`,
        );
      }
    }
    let applied: ReturnType<typeof applyEvent> | undefined;
    try {
      applied = applyEvent(from, event, graph);
    } catch (error) {
      addError(
        `component ${proof.componentId} event ${event.id} could not be replayed: ${String(error)}`,
      );
      continue;
    }
    if (applied === undefined) {
      continue;
    }
    if (applied.outcome !== transition.outcome) {
      addError(
        `component ${proof.componentId} event ${event.id} outcome mismatch`,
      );
    }
    const expectedRejection = applied.rejection?.code;
    if (expectedRejection !== transition.rejection) {
      addError(
        `component ${proof.componentId} event ${event.id} rejection mismatch`,
      );
    }
    if (applied.violation !== undefined) {
      addError(
        `component ${proof.componentId} contains a violating transition ${event.id}`,
      );
    }
    if (stateKey(applied.state) !== stateKey(to)) {
      addError(
        `component ${proof.componentId} event ${event.id} target state mismatch`,
      );
    }
  }

  const enabledEvents = new Set<EventId>();
  for (let stateId = 0; stateId < localStates.length; stateId += 1) {
    const state = localStates[stateId] as RuntimeState;
    for (const eventId of proof.events) {
      const event = graph.eventById.get(eventId);
      if (event === undefined) {
        addError(
          `component ${proof.componentId} references unknown event ${eventId}`,
        );
        continue;
      }
      let enabled = !state.executed.has(eventId);
      for (const dependency of event.before) {
        if (!state.executed.has(dependency)) {
          enabled = false;
        }
      }
      const edge = byId.get(transitionKey(stateId, eventId));
      if (enabled) {
        enabledEvents.add(eventId);
        if (edge === undefined) {
          addError(
            `component ${proof.componentId} is missing enabled edge ${stateId}/${eventId}`,
          );
        }
      } else if (edge !== undefined) {
        addError(
          `component ${proof.componentId} has edge for disabled event ${stateId}/${eventId}`,
        );
      }
    }
  }

  // Reachability of every claimed state from state 0.
  const reachable = new Set<number>([0]);
  const queue = [0];
  while (queue.length > 0) {
    const from = queue.shift() as number;
    for (const transition of outgoing.get(from) ?? []) {
      if (!reachable.has(transition.to)) {
        reachable.add(transition.to);
        queue.push(transition.to);
      }
    }
  }
  for (let stateId = 0; stateId < localStates.length; stateId += 1) {
    if (!reachable.has(stateId)) {
      addError(
        `component ${proof.componentId} state ${stateId} is not reachable`,
      );
    }
  }

  const covered = new Set(proof.transitions.map((entry) => entry.event));
  for (const event of proof.events) {
    if (!covered.has(event)) {
      addError(
        `component ${proof.componentId} never checks event ${event}`,
      );
    }
  }
  if (
    !Array.isArray(proof.checkedEvents) ||
    !sameSet(proof.checkedEvents, [...covered])
  ) {
    addError(
      `component ${proof.componentId} checkedEvents does not match its edges`,
    );
  }
  if (proof.enabledOccurrences !== proof.transitions.length) {
    addError(
      `component ${proof.componentId} enabledOccurrences does not match its edges`,
    );
  }

  return {
    states: localStates.length,
    transitions: proof.transitions.length,
    enabledEvents: enabledEvents.size,
  };
}

/**
 * Independently verify a state-closure certificate.  The checker does not
 * trust any transition: it re-applies the event and compares the resulting
 * state.  It also checks the closure condition, so deleting any feasible edge
 * (an enabled event from a reachable state) makes verification fail.
 */
export function verifySafetyProof(
  graph: unknown,
  proof: SafetyProof,
  options: AnalyzeOptions = {},
): ProofCheckResult {
  const errors: string[] = [];
  const addError = (message: string): void => {
    if (errors.length < 100) {
      errors.push(message);
    }
  };
  const emptyChecked = {
    components: 0,
    states: 0,
    transitions: 0,
    enabledEvents: 0,
    events: 0,
  };
  let normalized: NormalizedGraph;
  try {
    normalized = normalizeGraph(graph as Parameters<typeof normalizeGraph>[0], options);
  } catch (error) {
    return {
      ok: false,
      errors: [
        error instanceof GraphError ? error.message : `invalid graph: ${String(error)}`,
      ],
      checked: emptyChecked,
    };
  }
  if (proof === null || typeof proof !== "object") {
    return {
      ok: false,
      errors: ["proof is not an object"],
      checked: emptyChecked,
    };
  }
  if (proof.kind !== "state-closure" || proof.version !== 1) {
    return {
      ok: false,
      errors: ["unsupported proof kind or version"],
      checked: emptyChecked,
    };
  }
  if (proof.graphDigest !== normalized.digest) {
    addError("proof digest does not match the graph");
  }
  if (!Array.isArray(proof.components)) {
    addError("proof.components is not an array");
    return { ok: false, errors, checked: emptyChecked };
  }
  if (!Array.isArray(proof.eventPartition)) {
    addError("proof.eventPartition is not an array");
    return { ok: false, errors, checked: emptyChecked };
  }

  const expectedComponents = buildComponents(normalized);
  if (proof.eventPartition.length !== expectedComponents.length) {
    addError("event partition has the wrong number of components");
  }
  const usedExpected = new Set<number>();
  for (const partition of proof.eventPartition) {
    if (!Array.isArray(partition)) {
      addError("event partition contains a malformed group");
      continue;
    }
    let match = -1;
    for (const component of expectedComponents) {
      if (
        !usedExpected.has(component.id) &&
        sameSet(partition, component.eventIds)
      ) {
        match = component.id;
        break;
      }
    }
    if (match < 0) {
      addError("event partition contains a non-independent group");
    } else {
      usedExpected.add(match);
    }
  }
  for (const component of expectedComponents) {
    if (!usedExpected.has(component.id)) {
      addError(`event partition omits component with ${component.eventIds[0]}`);
    }
  }

  const seenComponents = new Set<number>();
  let totalStates = 0;
  let totalTransitions = 0;
  let totalEnabledEvents = 0;
  for (const componentProof of proof.components) {
    if (componentProof === null || typeof componentProof !== "object") {
      addError("proof contains a malformed component");
      continue;
    }
    let expected = expectedComponents.find((component) =>
      sameSet(component.eventIds, componentProof.events),
    );
    if (expected === undefined) {
      addError(
        `component ${componentProof.componentId} is not an expected component`,
      );
      continue;
    }
    if (seenComponents.has(expected.id)) {
      addError(`component ${expected.id} is duplicated`);
      continue;
    }
    seenComponents.add(expected.id);
    const checked = checkComponent(
      normalized,
      expected.eventIds,
      componentProof,
      addError,
    );
    totalStates += checked.states;
    totalTransitions += checked.transitions;
    totalEnabledEvents += checked.enabledEvents;
  }
  for (const component of expectedComponents) {
    if (!seenComponents.has(component.id)) {
      addError(`proof omits component ${component.id}`);
    }
  }

  if (
    proof.totals === null ||
    typeof proof.totals !== "object"
  ) {
    addError("proof totals is missing or malformed");
  } else {
    if (proof.totals.components !== proof.components.length) {
      addError("proof totals.components mismatch");
    }
    if (proof.totals.states !== totalStates) {
      addError("proof totals.states mismatch");
    }
    if (proof.totals.transitions !== totalTransitions) {
      addError("proof totals.transitions mismatch");
    }
    if (proof.totals.enabledOccurrences !== totalTransitions) {
      addError("proof totals.enabledOccurrences mismatch");
    }
    if (proof.totals.events !== normalized.events.length) {
      addError("proof totals.events mismatch");
    }
  }

  return {
    ok: errors.length === 0,
    errors,
    checked: {
      components: proof.components.length,
      states: totalStates,
      transitions: totalTransitions,
      enabledEvents: totalEnabledEvents,
      events: normalized.events.length,
    },
  };
}

export const checkSafetyProof = verifySafetyProof;
export const checkStateClosureProof = verifySafetyProof;
export const checkProof = verifySafetyProof;
export const verifyProof = verifySafetyProof;
