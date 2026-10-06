import type { ActionName, GraphVersion, InstanceId, StateName } from "./types";

export class WorkflowError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class UnknownGraphVersionError extends WorkflowError {
  constructor(readonly version: GraphVersion) {
    super(`No graph registered for version "${version}".`);
  }
}

export class UnknownInstanceError extends WorkflowError {
  constructor(readonly instanceId: InstanceId) {
    super(`No event log found for instance "${instanceId}".`);
  }
}

export class InstanceExistsError extends WorkflowError {
  constructor(readonly instanceId: InstanceId) {
    super(`Instance "${instanceId}" already exists.`);
  }
}

export class StaleVersionError extends WorkflowError {
  constructor(
    readonly instanceId: InstanceId,
    readonly expected: GraphVersion,
    readonly actual: GraphVersion,
  ) {
    super(
      `Command rejected for instance "${instanceId}": issued against version ` +
        `"${expected}" but the instance is on "${actual}".`,
    );
  }
}

export class InvalidTransitionError extends WorkflowError {
  constructor(
    readonly instanceId: InstanceId,
    readonly state: StateName,
    readonly action: ActionName,
  ) {
    super(
      `No transition from state "${state}" via action "${action}" ` +
        `for instance "${instanceId}".`,
    );
  }
}

export interface UnmappedActiveState {
  readonly instanceId: InstanceId;
  readonly state: StateName;
}

export class UnmappedStateError extends WorkflowError {
  constructor(readonly unmapped: readonly UnmappedActiveState[]) {
    super(
      "Migration aborted; unmapped active states: " +
        unmapped.map((u) => `"${u.instanceId}" in "${u.state}"`).join(", ") +
        ".",
    );
  }
}

export class CorruptLogError extends WorkflowError {
  constructor(message: string) {
    super(message);
  }
}
