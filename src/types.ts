export interface Transition {
  readonly event: string;
  readonly from: readonly string[];
  readonly to: string;
}

export interface GraphVersion {
  readonly version: number;
  readonly states: readonly string[];
  readonly initialState: string;
  readonly transitions: readonly Transition[];
  readonly stateMapping?: Readonly<Record<string, string>>;
}

export interface Command {
  readonly event: string;
  readonly graphVersion: number;
}

export interface CreatedEvent {
  readonly kind: "created";
  readonly seq: number;
  readonly at: number;
  readonly graphVersion: number;
  readonly state: string;
}

export interface TransitionEvent {
  readonly kind: "transition";
  readonly seq: number;
  readonly at: number;
  readonly graphVersion: number;
  readonly event: string;
  readonly from: string;
  readonly to: string;
}

export interface MigratedEvent {
  readonly kind: "migrated";
  readonly seq: number;
  readonly at: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly stateBefore: string;
  readonly stateAfter: string;
  readonly stateMapping: Readonly<Record<string, string>>;
}

export type InstanceEvent = CreatedEvent | TransitionEvent | MigratedEvent;

export interface InstanceSnapshot {
  readonly id: string;
  readonly graphVersion: number;
  readonly state: string;
  readonly log: readonly InstanceEvent[];
}

export interface MigrationFailure {
  readonly instanceId: string;
  readonly reason: string;
}

export interface MigrationResult {
  readonly instanceId: string;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly stateBefore: string;
  readonly stateAfter: string;
  readonly seq: number;
}
