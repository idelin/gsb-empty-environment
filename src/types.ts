export type StateName = string;
export type ActionName = string;
export type GraphVersion = string;
export type InstanceId = string;

export interface Transition {
  readonly from: StateName;
  readonly action: ActionName;
  readonly to: StateName;
}

export interface GraphSpec {
  readonly name: string;
  readonly version: GraphVersion;
  readonly initialState: StateName;
  readonly states: readonly StateName[];
  readonly transitions: readonly Transition[];
  readonly terminalStates?: readonly StateName[];
}

export interface WorkflowGraph {
  readonly name: string;
  readonly version: GraphVersion;
  readonly initialState: StateName;
  readonly states: ReadonlySet<StateName>;
  readonly transitions: readonly Transition[];
  readonly terminalStates: ReadonlySet<StateName>;
}

export interface CreatedPayload {
  readonly type: "created";
  readonly version: GraphVersion;
  readonly initialState: StateName;
}

export interface TransitionPayload {
  readonly type: "transition";
  readonly action: ActionName;
  readonly from: StateName;
  readonly to: StateName;
}

export type StateMapping = Readonly<Record<StateName, StateName>>;

export interface MigrationPayload {
  readonly type: "migration";
  readonly fromVersion: GraphVersion;
  readonly toVersion: GraphVersion;
  readonly fromState: StateName;
  readonly toState: StateName;
  /** Snapshot of the full mapping applied, kept for auditability. */
  readonly mapping: StateMapping;
}

export type EventPayload = CreatedPayload | TransitionPayload | MigrationPayload;

export interface EventEnvelope<P extends EventPayload = EventPayload> {
  readonly seq: number;
  readonly instanceId: InstanceId;
  readonly version: GraphVersion;
  readonly payload: P;
  readonly recordedAt: string;
}

export interface Command {
  readonly action: ActionName;
  /** Graph version the issuer believes the instance is on. */
  readonly expectedVersion: GraphVersion;
}
