import { findTransition, GraphRegistry } from "./graph";
import { replay, ReplayResult } from "./replay";
import { InMemoryEventStore } from "./store";
import {
  InstanceExistsError,
  InvalidTransitionError,
  StaleVersionError,
} from "./errors";
import type {
  Command,
  EventEnvelope,
  GraphVersion,
  InstanceId,
  TransitionPayload,
} from "./types";

export class WorkflowEngine {
  constructor(
    readonly registry: GraphRegistry,
    readonly store: InMemoryEventStore = new InMemoryEventStore(),
  ) {}

  createInstance(instanceId: InstanceId, version: GraphVersion): ReplayResult {
    if (this.store.has(instanceId)) {
      throw new InstanceExistsError(instanceId);
    }
    const graph = this.registry.get(version);
    this.store.append(instanceId, version, {
      type: "created",
      version,
      initialState: graph.initialState,
    });
    return this.inspect(instanceId);
  }

  /** Rehydrate an instance from a log persisted by an older deploy. */
  restoreInstance(
    instanceId: InstanceId,
    log: readonly EventEnvelope[],
  ): ReplayResult {
    if (this.store.has(instanceId)) {
      throw new InstanceExistsError(instanceId);
    }
    this.store.restore(instanceId, log);
    return this.inspect(instanceId);
  }

  inspect(instanceId: InstanceId): ReplayResult {
    return replay(this.registry, this.store.load(instanceId));
  }

  /**
   * Apply a command. Commands issued against a superseded graph version
   * (e.g. in-flight requests from before an upgrade) are rejected, so a
   * late command can never interleave with the new-version event stream.
   */
  dispatch(instanceId: InstanceId, command: Command): EventEnvelope<TransitionPayload> {
    const current = this.inspect(instanceId);
    if (command.expectedVersion !== current.version) {
      throw new StaleVersionError(instanceId, command.expectedVersion, current.version);
    }
    const graph = this.registry.get(current.version);
    const transition = findTransition(graph, current.state, command.action);
    if (!transition) {
      throw new InvalidTransitionError(instanceId, current.state, command.action);
    }
    return this.store.append(instanceId, current.version, {
      type: "transition",
      action: transition.action,
      from: transition.from,
      to: transition.to,
    });
  }
}
