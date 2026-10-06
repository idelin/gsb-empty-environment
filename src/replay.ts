import { availableActions, GraphRegistry } from "./graph";
import { CorruptLogError } from "./errors";
import type {
  ActionName,
  EventEnvelope,
  GraphVersion,
  StateName,
} from "./types";

export interface ReplayResult {
  readonly version: GraphVersion;
  readonly state: StateName;
  readonly terminal: boolean;
  readonly availableActions: readonly ActionName[];
}

/**
 * Fold an event log left-to-right, switching graph versions at migration
 * events. Pass `upToSeq` to inspect the instance as of an earlier point in
 * its history (e.g. just before an upgrade).
 */
export function replay(
  registry: GraphRegistry,
  events: readonly EventEnvelope[],
  upToSeq?: number,
): ReplayResult {
  let version: GraphVersion | undefined;
  let state: StateName | undefined;

  for (const envelope of events) {
    if (upToSeq !== undefined && envelope.seq > upToSeq) {
      break;
    }
    const payload = envelope.payload;
    switch (payload.type) {
      case "created":
        if (version !== undefined) {
          throw new CorruptLogError(
            `Duplicate "created" event at seq ${envelope.seq}.`,
          );
        }
        registry.get(payload.version);
        version = payload.version;
        state = payload.initialState;
        break;
      case "transition": {
        if (version === undefined || state === undefined) {
          throw new CorruptLogError(
            `Transition event at seq ${envelope.seq} precedes "created".`,
          );
        }
        if (payload.from !== state) {
          throw new CorruptLogError(
            `Transition event at seq ${envelope.seq} starts from ` +
              `"${payload.from}" but the replayed state is "${state}".`,
          );
        }
        state = payload.to;
        break;
      }
      case "migration": {
        if (version === undefined || state === undefined) {
          throw new CorruptLogError(
            `Migration event at seq ${envelope.seq} precedes "created".`,
          );
        }
        if (payload.fromVersion !== version || payload.fromState !== state) {
          throw new CorruptLogError(
            `Migration event at seq ${envelope.seq} expects ` +
              `${payload.fromVersion}@"${payload.fromState}" but the replayed ` +
              `position is ${version}@"${state}".`,
          );
        }
        registry.get(payload.toVersion);
        version = payload.toVersion;
        state = payload.toState;
        break;
      }
    }
  }

  if (version === undefined || state === undefined) {
    throw new CorruptLogError("Event log has no \"created\" event.");
  }
  const graph = registry.get(version);
  return {
    version,
    state,
    terminal: graph.terminalStates.has(state),
    availableActions: availableActions(graph, state),
  };
}
