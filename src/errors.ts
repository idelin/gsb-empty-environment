export class VersionConflictError extends Error {
  readonly expectedVersion: number;
  readonly actualVersion: number;

  constructor(message: string, expectedVersion: number, actualVersion: number) {
    super(message);
    this.name = 'VersionConflictError';
    this.expectedVersion = expectedVersion;
    this.actualVersion = actualVersion;
  }
}

export class CommandFailedError extends Error {
  readonly commandName: string;
  readonly rolledBack: string[];

  constructor(message: string, commandName: string, rolledBack: string[], cause?: unknown) {
    super(message, { cause });
    this.name = 'CommandFailedError';
    this.commandName = commandName;
    this.rolledBack = rolledBack;
  }
}
