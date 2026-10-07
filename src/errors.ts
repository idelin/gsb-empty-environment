import type { MigrationFailure } from "./types.ts";

export class WorkflowError extends Error {
  readonly code: string;

  constructor(code: string, message: string) {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export class UnknownGraphError extends WorkflowError {
  readonly version: number;

  constructor(version: number) {
    super("UNKNOWN_GRAPH", `No graph registered for version ${version}.`);
    this.version = version;
  }
}

export class UnknownInstanceError extends WorkflowError {
  readonly instanceId: string;

  constructor(instanceId: string) {
    super("UNKNOWN_INSTANCE", `Unknown workflow instance "${instanceId}".`);
    this.instanceId = instanceId;
  }
}

export class DuplicateInstanceError extends WorkflowError {
  readonly instanceId: string;

  constructor(instanceId: string) {
    super("DUPLICATE_INSTANCE", `Workflow instance "${instanceId}" already exists.`);
    this.instanceId = instanceId;
  }
}

export class StaleCommandError extends WorkflowError {
  readonly instanceId: string;
  readonly commandVersion: number;
  readonly currentVersion: number;

  constructor(instanceId: string, commandVersion: number, currentVersion: number) {
    super(
      "STALE_COMMAND",
      `Command for instance "${instanceId}" was issued against graph v${commandVersion} ` +
        `but the instance is on v${currentVersion}; late commands from old versions are rejected.`,
    );
    this.instanceId = instanceId;
    this.commandVersion = commandVersion;
    this.currentVersion = currentVersion;
  }
}

export class InvalidTransitionError extends WorkflowError {
  readonly instanceId: string;
  readonly event: string;
  readonly state: string;

  constructor(instanceId: string, event: string, state: string) {
    super(
      "INVALID_TRANSITION",
      `Event "${event}" is not allowed in state "${state}" of instance "${instanceId}".`,
    );
    this.instanceId = instanceId;
    this.event = event;
    this.state = state;
  }
}

export class MissingStateMappingError extends WorkflowError {
  readonly version: number;

  constructor(version: number) {
    super(
      "MISSING_STATE_MAPPING",
      `Graph v${version} does not declare a stateMapping from its previous version.`,
    );
    this.version = version;
  }
}

export class InvalidGraphError extends WorkflowError {
  constructor(message: string) {
    super("INVALID_GRAPH", message);
  }
}

export class CorruptHistoryError extends WorkflowError {
  constructor(message: string) {
    super("CORRUPT_HISTORY", message);
  }
}

export class BatchMigrationError extends WorkflowError {
  readonly toVersion: number;
  readonly failures: readonly MigrationFailure[];

  constructor(toVersion: number, failures: readonly MigrationFailure[]) {
    const detail = failures.map((f) => `"${f.instanceId}": ${f.reason}`).join("; ");
    super(
      "BATCH_MIGRATION_FAILED",
      `Batch migration to v${toVersion} aborted; no instance was migrated. Failures: ${detail}`,
    );
    this.toVersion = toVersion;
    this.failures = failures;
  }
}
