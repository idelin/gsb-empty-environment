import {
  GraphError,
  applyEvent,
  buildInitialState,
  normalizeGraph,
  type NormalizedGraph,
} from "./model.ts";
import type {
  AnalyzeOptions,
  EventId,
  StepRecord,
  ViolationDetail,
  ViolationWitness,
  WitnessCheckResult,
} from "./types.ts";

function violationEqual(
  left: ViolationDetail,
  right: ViolationDetail,
): boolean {
  return (
    left.event === right.event &&
    left.kind === right.kind &&
    left.objectId === right.objectId &&
    left.handle === right.handle &&
    left.path === right.path &&
    left.protectionClass === right.protectionClass &&
    left.contentLabel === right.contentLabel &&
    left.lockSerial === right.lockSerial
  );
}

/**
 * Replay a claimed violating prefix.  This checks feasibility and that the
 * final event really performs a successful read of complete-labelled content
 * while locked.  Minimality itself is established by `analyze`; use the
 * returned prefix directly when an exact answer is required.
 */
export function verifyWitness(
  graph: unknown,
  witness: ViolationWitness,
  options: AnalyzeOptions = {},
): WitnessCheckResult {
  const errors: string[] = [];
  let normalized: NormalizedGraph;
  try {
    normalized = normalizeGraph(
      graph as Parameters<typeof normalizeGraph>[0],
      options,
    );
  } catch (error) {
    return {
      ok: false,
      errors: [
        error instanceof GraphError ? error.message : `invalid graph: ${String(error)}`,
      ],
      steps: [],
    };
  }
  if (witness === null || typeof witness !== "object") {
    return { ok: false, errors: ["witness is not an object"], steps: [] };
  }
  if (!Array.isArray(witness.prefix)) {
    return { ok: false, errors: ["witness.prefix is not an array"], steps: [] };
  }
  if (witness.prefix.length === 0) {
    return { ok: false, errors: ["witness.prefix is empty"], steps: [] };
  }

  let state = buildInitialState(normalized);
  const steps: StepRecord[] = [];
  const seen = new Set<EventId>();
  let lastViolation: ViolationDetail | undefined;
  for (let index = 0; index < witness.prefix.length; index += 1) {
    const eventId = witness.prefix[index] as EventId;
    if (seen.has(eventId)) {
      errors.push(`witness executes ${eventId} twice`);
      break;
    }
    seen.add(eventId);
    const event = normalized.eventById.get(eventId);
    if (event === undefined) {
      errors.push(`witness references unknown event ${eventId}`);
      break;
    }
    for (const dependency of event.before) {
      if (!state.executed.has(dependency)) {
        errors.push(
          `witness executes ${eventId} before its predecessor ${dependency}`,
        );
      }
    }
    const result = applyEvent(state, event, normalized);
    state = result.state;
    const step: StepRecord = {
      event: eventId,
      outcome: result.outcome,
      rejection: result.rejection,
      violation: result.violation,
    };
    steps.push(step);
    if (result.violation !== undefined) {
      if (index !== witness.prefix.length - 1) {
        errors.push(
          `witness already violates at event ${eventId} before the last event`,
        );
      }
      lastViolation = result.violation;
    }
  }
  if (lastViolation === undefined) {
    errors.push("witness prefix never reads complete content while locked");
  } else if (
    witness.violation === undefined ||
    !violationEqual(lastViolation, witness.violation)
  ) {
    errors.push("witness violation detail does not match the replay");
  }
  if (!Array.isArray(witness.steps)) {
    errors.push("witness.steps is not an array");
  } else if (witness.steps.length !== steps.length) {
    errors.push("witness step count does not match the replay");
  } else {
    for (let index = 0; index < steps.length; index += 1) {
      const expected = steps[index] as StepRecord;
      const actual = witness.steps[index] as StepRecord;
      if (
        expected.event !== actual.event ||
        expected.outcome !== actual.outcome ||
        expected.rejection?.code !== actual.rejection?.code ||
        (expected.violation === undefined) !== (actual.violation === undefined)
      ) {
        errors.push(`witness step ${index} does not match the replay`);
      }
    }
  }
  return { ok: errors.length === 0, errors, steps };
}

export const verifyViolationWitness = verifyWitness;
export const checkWitness = verifyWitness;
