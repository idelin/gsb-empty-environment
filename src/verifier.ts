import {
  createAuthorizationContext,
  isInstanceAuthorized,
  privateMessagesForFrame,
  protectedMessagesForFrame,
} from "./authorization.ts";
import {
  compareStableIds,
  frameMustContinue,
  groupMustContinue,
  normalizeMigrationProblem,
  type NormalizedFrame,
  type NormalizedProblem,
} from "./model.ts";
import {
  MigrationInputError,
  MigrationVerificationError,
  type MigrationProblem,
  type PlanInput,
  type SignedEvent,
  type StableId,
  type UnsafeHistoryWitness,
  type VerificationResult,
} from "./types.ts";

export interface VerifyOptions {
  maxInterruptible?: number;
  /** Hard limit when callers explicitly enumerate compatible histories. */
  maxHistories?: number;
}

interface NormalizedPlan {
  reset: StableId[];
  continuations: Record<StableId, StableId> | null;
  frameContinuations: Record<StableId, StableId> | null;
  stoppedChannels: StableId[] | null;
  stoppedFrames: StableId[] | null;
  stoppedGroups: StableId[] | null;
}

interface DerivedPlan {
  feasible: boolean;
  reasons: string[];
  continuations: Map<StableId, StableId>;
  frameContinuations: Map<StableId, StableId>;
  stoppedChannels: Set<StableId>;
  stoppedFrames: Set<StableId>;
  stoppedGroups: Set<StableId>;
}

function asString(value: unknown): StableId | null {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return null;
}

function asStringArray(value: unknown): StableId[] | null {
  if (value === undefined || value === null) return null;
  if (Array.isArray(value)) {
    return value
      .map((entry) => asString(entry))
      .filter((entry): entry is StableId => entry !== null);
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    for (const nested of [
      record.reauthenticate,
      record.restart,
      record.items,
      record.values,
    ]) {
      const array = asStringArray(nested);
      if (array !== null) return array;
    }
  }
  const single = asString(value);
  return single === null ? null : [single];
}

function asMapping(value: unknown): Record<StableId, StableId> | null {
  if (value === undefined || value === null) return null;
  const record: Record<StableId, StableId> = {};
  if (value instanceof Map) {
    for (const [key, entry] of value.entries()) {
      const channel = asString(key);
      const instance = asString(entry);
      if (channel !== null && instance !== null) record[channel] = instance;
    }
    return record;
  }
  if (typeof value === "object") {
    for (const [key, entry] of Object.entries(value as Record<string, unknown>)) {
      const instance = asString(entry);
      if (instance !== null) record[key] = instance;
    }
  }
  return record;
}

function normalizePlan(plan: PlanInput): NormalizedPlan {
  if (Array.isArray(plan)) {
    return {
      reset: (asStringArray(plan) ?? []).slice().sort(compareStableIds),
      continuations: null,
      frameContinuations: null,
      stoppedChannels: null,
      stoppedFrames: null,
      stoppedGroups: null,
    };
  }
  const record = plan as unknown as Record<string, unknown>;
  const reset =
    asStringArray(record.reauthenticate) ??
    asStringArray(record.reauthenticated) ??
    asStringArray(record.reset) ??
    asStringArray(record.reAuth) ??
    asStringArray(record.interrupt) ??
    [];
  return {
    reset: reset.slice().sort(compareStableIds),
    continuations:
      asMapping(record.continuations) ??
      asMapping(record.channelContinuations) ??
      asMapping(record.mapping),
    frameContinuations:
      asMapping(record.frameContinuations) ??
      asMapping(record.frameMapping),
    stoppedChannels:
      asStringArray(record.stoppedChannels) ??
      asStringArray(record.interrupted) ??
      asStringArray(record.stopped),
    stoppedFrames: asStringArray(record.stoppedFrames),
    stoppedGroups:
      asStringArray(record.stoppedGroups) ??
      asStringArray(record.stopGroups),
  };
}

function addUnique(target: Set<StableId>, values: Iterable<StableId>): void {
  for (const value of values) target.add(value);
}

/**
 * This is intentionally a separate implementation from the planner. It is
 * used both to validate a concrete mapping and to search for a smaller reset
 * set when checking optimality.
 */
function derivePlan(model: NormalizedProblem, reset: ReadonlySet<StableId>): DerivedPlan {
  const reasons: string[] = [];
  const context = createAuthorizationContext(model, reset);
  const continuations = new Map<StableId, StableId>();
  const frameContinuations = new Map<StableId, StableId>();
  const stoppedChannels = new Set<StableId>();
  const stoppedFrames = new Set<StableId>();
  const stoppedGroups = new Set<StableId>();
  let feasible = true;

  const protectedOnChannel = (channelId: StableId): boolean =>
    model.messages.some(
      (message) => message.protected && message.channelIds.includes(channelId),
    );

  const stopChannel = (channelId: StableId, why: string): void => {
    if (stoppedChannels.has(channelId)) return;
    const channel = model.channels.get(channelId);
    if (channel === undefined) {
      stoppedChannels.add(channelId);
      return;
    }
    if (channel.required) {
      reasons.push(`${why}: required channel ${channelId} cannot stop`);
      feasible = false;
      return;
    }
    if (protectedOnChannel(channelId)) {
      reasons.push(`${why}: channel ${channelId} carries protected public traffic`);
      feasible = false;
      return;
    }
    stoppedChannels.add(channelId);
  };

  const stopFrame = (frameId: StableId, why: string): void => {
    if (stoppedFrames.has(frameId)) return;
    if (frameMustContinue(model, frameId)) {
      reasons.push(`${why}: frame ${frameId} must continue`);
      feasible = false;
      return;
    }
    stoppedFrames.add(frameId);
    for (const channelId of model.frames.get(frameId)?.channelIds ?? []) {
      stopChannel(channelId, why);
      if (!feasible) return;
    }
  };

  for (const channel of model.channels.values()) {
    if (channel.stop) stopChannel(channel.id, "channel was explicitly stopped");
    if (!feasible) break;
  }

  for (const frameId of model.frameOrder) {
    if (!feasible) break;
    const frame = model.frames.get(frameId) as NormalizedFrame;
    const remaining = frame.domain.filter((instanceId) => !reset.has(instanceId));
    const privateMessages = privateMessagesForFrame(model, frameId);

    if (remaining.length === 0) {
      if (frameMustContinue(model, frameId)) {
        reasons.push(`frame ${frameId} has no remaining candidate`);
        feasible = false;
        break;
      }
      stopFrame(frameId, "no candidate remains");
      continue;
    }

    let unsafe: UnsafeHistoryWitness | null = null;
    for (const instanceId of remaining) {
      for (const message of privateMessages) {
        if (!isInstanceAuthorized(context, instanceId, message)) {
          unsafe = { frame: frameId, message: message.id, instance: instanceId };
          break;
        }
      }
      if (unsafe !== null) break;
    }

    if (unsafe !== null) {
      if (frameMustContinue(model, frameId)) {
        reasons.push(
          `frame ${frameId} has unsafe compatible history ${unsafe.instance} for ${unsafe.message}`,
        );
        feasible = false;
        break;
      }
      stopFrame(frameId, "compatible history would leak a private reply");
      continue;
    }

    frameContinuations.set(frameId, remaining[0] as StableId);
    for (const channelId of frame.channelIds) {
      if (stoppedChannels.has(channelId)) continue;
      continuations.set(channelId, remaining[0] as StableId);
    }
  }

  let changed = true;
  while (feasible && changed) {
    changed = false;
    for (const group of model.groups) {
      if (!feasible) break;
      if (!group.atomic) continue;
      const memberStopped =
        group.frameIds.some((frameId) => stoppedFrames.has(frameId)) ||
        group.channelIds.some((channelId) => stoppedChannels.has(channelId));
      if (!memberStopped) continue;
      if (groupMustContinue(model, group)) {
        reasons.push(`mutex group ${group.id} would stop although it must continue`);
        feasible = false;
        break;
      }
      stoppedGroups.add(group.id);
      for (const frameId of group.frameIds) {
        if (!stoppedFrames.has(frameId)) {
          stopFrame(frameId, `mutex group ${group.id} is atomic`);
          changed = true;
        }
        if (!feasible) break;
      }
      for (const channelId of group.channelIds) {
        if (!stoppedChannels.has(channelId)) {
          stopChannel(channelId, `mutex group ${group.id} is atomic`);
          changed = true;
        }
        if (!feasible) break;
      }
    }
  }

  if (feasible) {
    for (const frameId of model.frameOrder) {
      if (stoppedFrames.has(frameId)) {
        frameContinuations.delete(frameId);
        for (const channelId of model.frames.get(frameId)?.channelIds ?? []) {
          continuations.delete(channelId);
        }
        continue;
      }
      const frame = model.frames.get(frameId) as NormalizedFrame;
      if (
        frame.channelIds.length > 0 &&
        frame.channelIds.every((channelId) => stoppedChannels.has(channelId))
      ) {
        stopFrame(frameId, "all channels are stopped");
        frameContinuations.delete(frameId);
      }
    }
    for (const group of model.groups) {
      if (
        group.frameIds.some((frameId) => stoppedFrames.has(frameId)) ||
        group.channelIds.some((channelId) => stoppedChannels.has(channelId))
      ) {
        stoppedGroups.add(group.id);
      }
    }
  }

  return {
    feasible,
    reasons,
    continuations,
    frameContinuations,
    stoppedChannels,
    stoppedFrames,
    stoppedGroups,
  };
}

function* combinations(
  values: readonly StableId[],
  size: number,
  start = 0,
  prefix: StableId[] = [],
): Generator<StableId[]> {
  if (prefix.length === size) {
    yield [...prefix];
    return;
  }
  const needed = size - prefix.length;
  for (let index = start; index <= values.length - needed; index += 1) {
    prefix.push(values[index] as StableId);
    yield* combinations(values, size, index + 1, prefix);
    prefix.pop();
  }
}

function interruptibleCandidates(model: NormalizedProblem): StableId[] {
  const candidates = new Set<StableId>();
  for (const frame of model.frames.values()) {
    for (const instanceId of frame.domain) candidates.add(instanceId);
  }
  return model.instanceOrder.filter((instanceId) => {
    const instance = model.instances.get(instanceId);
    return instance !== undefined && instance.interruptible && candidates.has(instanceId);
  });
}

function compareResetSets(left: readonly StableId[], right: readonly StableId[]): number {
  const length = Math.min(left.length, right.length);
  for (let index = 0; index < length; index += 1) {
    const order = compareStableIds(left[index] as StableId, right[index] as StableId);
    if (order !== 0) return order;
  }
  return left.length - right.length;
}

function sortedUnique(values: Iterable<StableId>): StableId[] {
  return [...new Set(values)].sort(compareStableIds);
}

function mappingObject(map: ReadonlyMap<StableId, StableId>): Record<StableId, StableId> {
  const result: Record<StableId, StableId> = {};
  for (const key of [...map.keys()].sort(compareStableIds)) {
    result[key] = map.get(key) as StableId;
  }
  return result;
}

function safePrivateMessages(
  model: NormalizedProblem,
  reset: ReadonlySet<StableId>,
  frameId: StableId,
): UnsafeHistoryWitness | null {
  const frame = model.frames.get(frameId);
  if (frame === undefined) return null;
  const context = createAuthorizationContext(model, reset);
  for (const instanceId of frame.domain) {
    if (reset.has(instanceId)) continue;
    for (const message of privateMessagesForFrame(model, frameId)) {
      if (!isInstanceAuthorized(context, instanceId, message)) {
        return { frame: frameId, message: message.id, instance: instanceId };
      }
    }
  }
  return null;
}

function canonicalStopClosure(
  model: NormalizedProblem,
  initialFrames: Iterable<StableId>,
  initialChannels: Iterable<StableId>,
): {
  feasible: boolean;
  reasons: string[];
  stoppedFrames: Set<StableId>;
  stoppedChannels: Set<StableId>;
  stoppedGroups: Set<StableId>;
} {
  const stoppedFrames = new Set(initialFrames);
  const stoppedChannels = new Set(initialChannels);
  const stoppedGroups = new Set<StableId>();
  const reasons: string[] = [];
  let feasible = true;

  const addChannel = (channelId: StableId, why: string): void => {
    if (stoppedChannels.has(channelId)) return;
    const channel = model.channels.get(channelId);
    if (channel?.required) {
      reasons.push(`${why}: required channel ${channelId} cannot stop`);
      feasible = false;
      return;
    }
    if (
      model.messages.some(
        (message) => message.protected && message.channelIds.includes(channelId),
      )
    ) {
      reasons.push(`${why}: channel ${channelId} carries protected public traffic`);
      feasible = false;
      return;
    }
    stoppedChannels.add(channelId);
  };

  const addFrame = (frameId: StableId, why: string): void => {
    if (stoppedFrames.has(frameId)) return;
    if (frameMustContinue(model, frameId)) {
      reasons.push(`${why}: frame ${frameId} must continue`);
      feasible = false;
      return;
    }
    stoppedFrames.add(frameId);
    for (const channelId of model.frames.get(frameId)?.channelIds ?? []) {
      addChannel(channelId, why);
      if (!feasible) return;
    }
  };

  let changed = true;
  while (feasible && changed) {
    changed = false;
    for (const group of model.groups) {
      if (!group.atomic) continue;
      const memberStopped =
        group.frameIds.some((frameId) => stoppedFrames.has(frameId)) ||
        group.channelIds.some((channelId) => stoppedChannels.has(channelId));
      if (!memberStopped) continue;
      if (groupMustContinue(model, group)) {
        reasons.push(`mutex group ${group.id} must continue but a member stops`);
        feasible = false;
        break;
      }
      stoppedGroups.add(group.id);
      for (const frameId of group.frameIds) {
        if (!stoppedFrames.has(frameId)) {
          addFrame(frameId, `mutex group ${group.id} is atomic`);
          changed = true;
        }
        if (!feasible) break;
      }
      for (const channelId of group.channelIds) {
        if (!stoppedChannels.has(channelId)) {
          addChannel(channelId, `mutex group ${group.id} is atomic`);
          changed = true;
        }
        if (!feasible) break;
      }
    }
    if (!feasible) break;
    for (const frameId of model.frameOrder) {
      const frame = model.frames.get(frameId);
      if (frame === undefined || stoppedFrames.has(frameId) || frame.channelIds.length === 0) {
        continue;
      }
      if (frame.channelIds.every((channelId) => stoppedChannels.has(channelId))) {
        addFrame(frameId, "all channels are stopped");
        changed = true;
      }
      if (!feasible) break;
    }
  }

  if (feasible) {
    for (const group of model.groups) {
      if (
        group.frameIds.some((frameId) => stoppedFrames.has(frameId)) ||
        group.channelIds.some((channelId) => stoppedChannels.has(channelId))
      ) {
        stoppedGroups.add(group.id);
      }
    }
  }

  return { feasible, reasons, stoppedFrames, stoppedChannels, stoppedGroups };
}

function findSmallerReset(
  model: NormalizedProblem,
  reset: readonly StableId[],
  options: VerifyOptions,
): { smaller: StableId[] | null; lexicographicallySmaller: StableId[] | null; canonical: boolean } {
  const interruptible = interruptibleCandidates(model);
  const max = options.maxInterruptible ?? 18;
  if (interruptible.length > max) {
    throw new MigrationInputError(
      `Optimality check supports at most ${max} interruptible receiver instances`,
    );
  }

  const size = reset.length;
  let smaller: StableId[] | null = null;
  let lexicographicallySmaller: StableId[] | null = null;
  let firstAtPlanSize: StableId[] | null = null;

  for (let candidateSize = 0; candidateSize <= size; candidateSize += 1) {
    for (const candidate of combinations(interruptible, candidateSize)) {
      if (!derivePlan(model, new Set(candidate)).feasible) continue;
      if (candidateSize < size) {
        if (smaller === null) smaller = candidate;
        continue;
      }
      if (firstAtPlanSize === null) firstAtPlanSize = candidate;
      if (compareResetSets(candidate, reset) < 0) {
        lexicographicallySmaller = candidate;
      }
      break;
    }
    if (smaller !== null) break;
  }

  const planIsFeasible = firstAtPlanSize !== null && compareResetSets(firstAtPlanSize, reset) === 0;
  return {
    smaller,
    lexicographicallySmaller,
    canonical: smaller === null && lexicographicallySmaller === null && planIsFeasible,
  };
}

export function verifyMigration(
  input: MigrationProblem | readonly SignedEvent[],
  planInput: PlanInput,
  options: VerifyOptions = {},
): VerificationResult {
  const errors: string[] = [];
  const unsafeHistories: UnsafeHistoryWitness[] = [];
  let model: NormalizedProblem;
  try {
    model = normalizeMigrationProblem(input);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return {
      ok: false,
      valid: false,
      safe: false,
      preservesPublicMessages: false,
      optimal: false,
      canonical: false,
      errors: [message],
      witnesses: {
        unsafeHistories: [],
        smallerReset: null,
        lexicographicallySmallerReset: null,
      },
      reauthenticate: [],
      continuations: {},
      frameContinuations: {},
      stoppedChannels: [],
      stoppedFrames: [],
      stoppedGroups: [],
    };
  }

  const plan = normalizePlan(planInput);
  if (new Set(plan.reset).size !== plan.reset.length) {
    errors.push("re-authentication set contains duplicate instances");
  }
  for (const instanceId of plan.reset) {
    const instance = model.instances.get(instanceId);
    if (instance === undefined) {
      errors.push(`unknown re-authentication instance ${instanceId}`);
      continue;
    }
    if (!instance.interruptible) {
      errors.push(`instance ${instanceId} is not interruptible`);
    }
  }

  const reset = new Set(plan.reset);
  const derived = derivePlan(model, reset);
  if (!derived.feasible) {
    errors.push(...derived.reasons);
  }

  const explicitStops =
    plan.stoppedChannels !== null ||
    plan.stoppedFrames !== null ||
    plan.stoppedGroups !== null;
  let stoppedChannels = new Set(derived.stoppedChannels);
  let stoppedFrames = new Set(derived.stoppedFrames);
  let stoppedGroups = new Set(derived.stoppedGroups);

  if (explicitStops) {
    for (const frameId of plan.stoppedFrames ?? []) {
      if (!model.frames.has(frameId)) errors.push(`unknown stopped frame ${frameId}`);
      stoppedFrames.add(frameId);
    }
    for (const channelId of plan.stoppedChannels ?? []) {
      if (!model.channels.has(channelId)) errors.push(`unknown stopped channel ${channelId}`);
      stoppedChannels.add(channelId);
    }
    for (const groupId of plan.stoppedGroups ?? []) {
      const group = model.groups.find((entry) => entry.id === groupId);
      if (group === undefined) {
        errors.push(`unknown stopped mutex group ${groupId}`);
        continue;
      }
      stoppedGroups.add(groupId);
      addUnique(stoppedFrames, group.frameIds);
      addUnique(stoppedChannels, group.channelIds);
    }
    const closure = canonicalStopClosure(model, stoppedFrames, stoppedChannels);
    if (!closure.feasible) errors.push(...closure.reasons);
    stoppedFrames = closure.stoppedFrames;
    stoppedChannels = closure.stoppedChannels;
    addUnique(stoppedGroups, closure.stoppedGroups);
  }

  const explicitMapping =
    plan.continuations !== null || plan.frameContinuations !== null;
  const continuations = new Map<StableId, StableId>();
  const frameContinuations = new Map<StableId, StableId>();

  if (explicitMapping) {
    for (const [channelId, instanceId] of Object.entries(plan.continuations ?? {})) {
      if (!model.channels.has(channelId)) {
        errors.push(`continuation refers to unknown channel ${channelId}`);
        continue;
      }
      continuations.set(channelId, instanceId);
    }
    for (const [frameId, instanceId] of Object.entries(plan.frameContinuations ?? {})) {
      if (!model.frames.has(frameId)) {
        errors.push(`frame continuation refers to unknown frame ${frameId}`);
        continue;
      }
      frameContinuations.set(frameId, instanceId);
    }
  } else {
    for (const [key, value] of derived.continuations) continuations.set(key, value);
    for (const [key, value] of derived.frameContinuations) frameContinuations.set(key, value);
  }

  for (const frameId of model.frameOrder) {
    const frame = model.frames.get(frameId) as NormalizedFrame;
    const isStopped = stoppedFrames.has(frameId);
    const frameMap = frameContinuations.get(frameId);
    const channelMaps = frame.channelIds
      .map((channelId) => ({
        channelId,
        instanceId: continuations.get(channelId),
        stopped: stoppedChannels.has(channelId),
      }))
      .filter((entry) => !entry.stopped);

    if (isStopped) {
      frameContinuations.delete(frameId);
      if (frameMap !== undefined && !stoppedChannels.has(frameMap)) {
        errors.push(`stopped frame ${frameId} still has a continuation`);
      }
      for (const entry of channelMaps) {
        errors.push(`stopped frame ${frameId} still maps channel ${entry.channelId}`);
      }
      continue;
    }

    const allChannelMaps = frame.channelIds.map((channelId) => ({
      channelId,
      instanceId: continuations.get(channelId),
      stopped: stoppedChannels.has(channelId),
    }));
    for (const entry of allChannelMaps) {
      if (entry.stopped && entry.instanceId !== undefined) {
        errors.push(`stopped channel ${entry.channelId} still has a continuation`);
      }
    }

    const remaining = frame.domain.filter((instanceId) => !reset.has(instanceId));
    const first = frameMap ?? channelMaps[0]?.instanceId;
    if (first !== undefined) {
      if (frameMap === undefined) frameContinuations.set(frameId, first);
      for (const entry of channelMaps) {
        if (entry.instanceId === undefined) {
          errors.push(`channel ${entry.channelId} has no continuation`);
          continue;
        }
        if (entry.instanceId !== first) {
          errors.push(
            `frame ${frameId} uses different instances (${first} and ${entry.instanceId}); a frame must choose one receiver`,
          );
        }
      }
    }

    if (remaining.length === 0) {
      errors.push(`frame ${frameId} has no non-reauthenticated candidate`);
      continue;
    }
    if (channelMaps.length === 0 && frame.channelIds.length > 0) {
      errors.push(`frame ${frameId} has no uninterrupted channel mapping`);
      continue;
    }
    if (first === undefined) {
      if (frameMustContinue(model, frameId)) {
        errors.push(`frame ${frameId} must continue but has no continuation`);
      }
      continue;
    }
    if (!remaining.includes(first)) {
      errors.push(`frame ${frameId} continuation ${first} is not an available candidate`);
    }
    for (const entry of channelMaps) {
      if (entry.instanceId === undefined) continue;
      if (!remaining.includes(entry.instanceId)) {
        errors.push(
          `channel ${entry.channelId} continues on unavailable instance ${entry.instanceId}`,
        );
      }
    }

    const witness = safePrivateMessages(model, reset, frameId);
    if (witness !== null) {
      unsafeHistories.push(witness);
      errors.push(
        `compatible history ${frameId}->${witness.instance} would deliver private message ${witness.message} to an unauthorized principal`,
      );
    }
  }

  let preservesPublicMessages = true;
  for (const messageId of model.protectedPublicMessages) {
    const message = model.messages.find((entry) => entry.id === messageId);
    if (message === undefined) continue;
    if (stoppedFrames.has(message.frameId)) {
      errors.push(`protected public message ${messageId} is in a stopped frame`);
      preservesPublicMessages = false;
      continue;
    }
    if (message.channelIds.length > 0) {
      const delivered = message.channelIds.some(
        (channelId) => !stoppedChannels.has(channelId) && continuations.has(channelId),
      );
      if (!delivered) {
        errors.push(`protected public message ${messageId} has no uninterrupted channel`);
        preservesPublicMessages = false;
      }
    } else if (!frameContinuations.has(message.frameId)) {
      errors.push(`protected public message ${messageId} has no frame continuation`);
      preservesPublicMessages = false;
    }
  }

  for (const group of model.groups) {
    const memberStopped =
      group.frameIds.some((frameId) => stoppedFrames.has(frameId)) ||
      group.channelIds.some((channelId) => stoppedChannels.has(channelId));
    if (!memberStopped) continue;
    if (groupMustContinue(model, group)) {
      errors.push(`mutex group ${group.id} is stopped but must continue`);
    }
    if (!group.atomic) continue;
    const allStopped =
      group.frameIds.every((frameId) => stoppedFrames.has(frameId)) &&
      group.channelIds.every((channelId) => stoppedChannels.has(channelId));
    if (!allStopped) {
      errors.push(`mutex group ${group.id} is only partially stopped`);
    }
  }

  let optimality: {
    smaller: StableId[] | null;
    lexicographicallySmaller: StableId[] | null;
    canonical: boolean;
  } = {
    smaller: null,
    lexicographicallySmaller: null,
    canonical: false,
  };
  try {
    optimality = findSmallerReset(model, plan.reset, options);
  } catch (error) {
    errors.push(error instanceof Error ? error.message : String(error));
  }
  const valid = errors.length === 0 && derived.feasible;
  const safe = unsafeHistories.length === 0 && derived.feasible;
  const publicPreserved = preservesPublicMessages;
  const optimal = optimality.smaller === null;
  const canonical = optimality.canonical;

  return {
    ok: valid && safe && publicPreserved && optimal && canonical,
    valid,
    safe,
    preservesPublicMessages: publicPreserved,
    optimal,
    canonical,
    errors,
    witnesses: {
      unsafeHistories,
      smallerReset: optimality.smaller,
      lexicographicallySmallerReset: optimality.lexicographicallySmaller,
    },
    reauthenticate: sortedUnique(plan.reset),
    continuations: mappingObject(continuations),
    frameContinuations: mappingObject(frameContinuations),
    stoppedChannels: sortedUnique(stoppedChannels),
    stoppedFrames: sortedUnique(stoppedFrames),
    stoppedGroups: sortedUnique(stoppedGroups),
  };
}

export const verifyMigrationPlan = verifyMigration;
export const verifyPlan = verifyMigration;
export const acceptMigrationPlan = verifyMigration;

export function assertMigrationPlan(
  input: MigrationProblem | readonly SignedEvent[],
  plan: PlanInput,
  options: VerifyOptions = {},
): VerificationResult {
  const result = verifyMigration(input, plan, options);
  if (!result.ok) {
    throw new MigrationVerificationError(result);
  }
  return result;
}

/**
 * Enumerates every compatible history of frame-level receiver attribution.
 * Each yielded object uses one instance per frame, so queues in one frame can
 * never independently select different identities.
 */
export function* enumerateCompatibleHistories(
  input: MigrationProblem | readonly SignedEvent[],
  resetInput: Iterable<StableId> = [],
  options: VerifyOptions = {},
): Generator<Record<StableId, StableId>> {
  const model = normalizeMigrationProblem(input);
  const reset = new Set(resetInput);
  const choices = model.frameOrder.map((frameId) => {
    const frame = model.frames.get(frameId) as NormalizedFrame;
    return {
      frameId,
      domain: frame.domain.filter((instanceId) => !reset.has(instanceId)),
    };
  });
  const limit = options.maxHistories ?? 100_000;
  let emitted = 0;
  const assignment: Record<StableId, StableId> = {};

  function* walk(index: number): Generator<Record<StableId, StableId>> {
    if (index === choices.length) {
      emitted += 1;
      if (emitted > limit) {
        throw new MigrationInputError(
          `Compatible-history enumeration exceeded ${limit} histories`,
        );
      }
      yield { ...assignment };
      return;
    }
    const choice = choices[index];
    if (choice === undefined) return;
    if (choice.domain.length === 0) return;
    for (const instanceId of choice.domain) {
      assignment[choice.frameId] = instanceId;
      yield* walk(index + 1);
    }
    delete assignment[choice.frameId];
  }

  yield* walk(0);
}
