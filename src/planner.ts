import {
  createAuthorizationContext,
  isInstanceAuthorized,
  privateMessagesForFrame,
  protectedMessagesForFrame,
} from "./authorization.ts";
import {
  channelsForFrame,
  compareStableIds,
  frameMustContinue,
  groupMustContinue,
  normalizeMigrationProblem,
  type NormalizedFrame,
  type NormalizedProblem,
} from "./model.ts";
import {
  MigrationInputError,
  NoFeasiblePlanError,
  type MigrationPlan,
  type MigrationProblem,
  type SignedEvent,
  type StableId,
} from "./types.ts";

export interface PlanOptions {
  /** Refuse to search beyond this many interruptible instances. */
  maxInterruptible?: number;
}

export interface ResetEvaluation {
  feasible: boolean;
  reasons: string[];
  frameContinuations: Map<StableId, StableId>;
  continuations: Map<StableId, StableId>;
  stoppedFrames: Set<StableId>;
  stoppedChannels: Set<StableId>;
  stoppedGroups: Set<StableId>;
}

function addReason(reasons: string[], reason: string): void {
  if (reasons.length < 8) reasons.push(reason);
}

function allFrameChannelsStopped(
  model: NormalizedProblem,
  frame: NormalizedFrame,
  stoppedChannels: ReadonlySet<StableId>,
): boolean {
  if (frame.channelIds.length === 0) return false;
  return frame.channelIds.every((channelId) => stoppedChannels.has(channelId));
}

/**
 * Computes whether a concrete reset set admits a safe migration. Safety is
 * history-quantified: every instance left in a frame's candidate domain must
 * be authorized for every private message in that frame. The same chosen
 * continuation is reused by every channel in the frame.
 */
export function evaluateReset(
  model: NormalizedProblem,
  resetInput: Iterable<StableId>,
): ResetEvaluation {
  const reset = new Set(resetInput);
  const reasons: string[] = [];
  const context = createAuthorizationContext(model, reset);
  const continuations = new Map<StableId, StableId>();
  const frameContinuations = new Map<StableId, StableId>();
  const stoppedFrames = new Set<StableId>();
  const stoppedChannels = new Set<StableId>();
  const stoppedGroups = new Set<StableId>();
  let feasible = true;

  const protectedUsesChannel = (channelId: StableId): boolean =>
    model.messages.some(
      (message) =>
        message.protected && message.channelIds.includes(channelId),
    );

  const stopChannel = (channelId: StableId, why: string): void => {
    if (stoppedChannels.has(channelId)) return;
    const channel = model.channels.get(channelId);
    if (channel === undefined) {
      stoppedChannels.add(channelId);
      return;
    }
    if (channel.required) {
      addReason(reasons, `${why}: required channel ${channelId} would stop`);
      feasible = false;
      return;
    }
    if (protectedUsesChannel(channelId)) {
      addReason(
        reasons,
        `${why}: channel ${channelId} carries a protected confirmed public message`,
      );
      feasible = false;
      return;
    }
    stoppedChannels.add(channelId);
  };

  const stopFrame = (frameId: StableId, why: string): void => {
    if (stoppedFrames.has(frameId)) return;
    if (frameMustContinue(model, frameId)) {
      addReason(reasons, `${why}: frame ${frameId} must continue`);
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
    if (channel.stop) stopChannel(channel.id, "explicitly stopped");
    if (!feasible) break;
  }

  for (const frameId of model.frameOrder) {
    if (!feasible) break;
    const frame = model.frames.get(frameId) as NormalizedFrame;
    const remaining = frame.domain.filter((instanceId) => !reset.has(instanceId));
    const privateMessages = privateMessagesForFrame(model, frameId);
    const protectedMessages = protectedMessagesForFrame(model, frameId);

    if (protectedMessages.length > 0 && remaining.length === 0) {
      addReason(
        reasons,
        `reset [${[...reset].join(", ")}] leaves no candidate for protected message frame ${frameId}`,
      );
      feasible = false;
      break;
    }

    if (remaining.length === 0) {
      if (frameMustContinue(model, frameId)) {
        addReason(
          reasons,
          `reset [${[...reset].join(", ")}] leaves frame ${frameId} without a continuation`,
        );
        feasible = false;
        break;
      }
      stopFrame(frameId, "no candidate remains");
      continue;
    }

    let badInstance: StableId | null = null;
    let badMessage: StableId | null = null;
    for (const instanceId of remaining) {
      for (const message of privateMessages) {
        if (!isInstanceAuthorized(context, instanceId, message)) {
          badInstance = instanceId;
          badMessage = message.id;
          break;
        }
      }
      if (badInstance !== null) break;
    }

    if (badInstance !== null) {
      if (frameMustContinue(model, frameId)) {
        addReason(
          reasons,
          `compatible history ${frameId}->${badInstance} would deliver private message ${badMessage} to an unauthorized principal`,
        );
        feasible = false;
        break;
      }
      stopFrame(frameId, "unsafe compatible history");
      continue;
    }

    const chosen = remaining[0] as StableId;
    frameContinuations.set(frameId, chosen);
    for (const channel of channelsForFrame(model, frameId)) {
      if (stoppedChannels.has(channel.id)) continue;
      continuations.set(channel.id, chosen);
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
        addReason(
          reasons,
          `mutex group ${group.id} must continue but one of its members is stopped`,
        );
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

  if (!feasible) {
    return {
      feasible: false,
      reasons,
      frameContinuations,
      continuations,
      stoppedFrames,
      stoppedChannels,
      stoppedGroups,
    };
  }

  for (const frameId of model.frameOrder) {
    if (stoppedFrames.has(frameId)) {
      frameContinuations.delete(frameId);
      for (const channelId of model.frames.get(frameId)?.channelIds ?? []) {
        continuations.delete(channelId);
      }
      continue;
    }
    const frame = model.frames.get(frameId) as NormalizedFrame;
    if (allFrameChannelsStopped(model, frame, stoppedChannels)) {
      stopFrame(frameId, "all of its channels are stopped");
      if (!feasible) break;
      frameContinuations.delete(frameId);
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

  return {
    feasible,
    reasons,
    frameContinuations,
    continuations,
    stoppedFrames,
    stoppedChannels,
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

function toPlan(reset: StableId[], evaluation: ResetEvaluation): MigrationPlan {
  const continuations: Record<StableId, StableId> = {};
  const frameContinuations: Record<StableId, StableId> = {};
  for (const channelId of [...evaluation.continuations.keys()].sort(compareStableIds)) {
    continuations[channelId] = evaluation.continuations.get(channelId) as StableId;
  }
  for (const frameId of [...evaluation.frameContinuations.keys()].sort(compareStableIds)) {
    frameContinuations[frameId] = evaluation.frameContinuations.get(frameId) as StableId;
  }
  return {
    reauthenticate: [...reset].sort(compareStableIds),
    continuations,
    frameContinuations,
    stoppedChannels: [...evaluation.stoppedChannels].sort(compareStableIds),
    stoppedFrames: [...evaluation.stoppedFrames].sort(compareStableIds),
    stoppedGroups: [...evaluation.stoppedGroups].sort(compareStableIds),
  };
}

export function planMigration(
  input: MigrationProblem | readonly SignedEvent[],
  options: PlanOptions = {},
): MigrationPlan {
  const model = normalizeMigrationProblem(input);
  if (model.instanceOrder.length === 0) {
    throw new MigrationInputError("The migration log contains no receiver instances");
  }

  const candidateInstances = new Set<StableId>();
  for (const frame of model.frames.values()) {
    for (const instanceId of frame.domain) candidateInstances.add(instanceId);
  }

  const interruptible = model.instanceOrder.filter((instanceId) => {
    const instance = model.instances.get(instanceId);
    return (
      instance !== undefined &&
      instance.interruptible &&
      candidateInstances.has(instanceId)
    );
  });
  const maxInterruptible = options.maxInterruptible ?? 18;
  if (interruptible.length > maxInterruptible) {
    throw new MigrationInputError(
      `Expected at most ${maxInterruptible} interruptible receiver instances, got ${interruptible.length}`,
    );
  }

  const lastReasons: string[] = [];
  for (let size = 0; size <= interruptible.length; size += 1) {
    for (const reset of combinations(interruptible, size)) {
      const evaluation = evaluateReset(model, reset);
      if (evaluation.feasible) return toPlan(reset, evaluation);
      for (const reason of evaluation.reasons) {
        if (lastReasons.length < 8) lastReasons.push(reason);
      }
    }
  }

  throw new NoFeasiblePlanError(
    "No re-authentication set can preserve the protected public messages and make every compatible private reply safe",
    lastReasons,
  );
}

export const planMigrationAuthorization = planMigration;
export const planReauthentication = planMigration;
export const solveMigration = planMigration;
