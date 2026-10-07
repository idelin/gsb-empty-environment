export class ExpressionSyntaxError extends Error {
  readonly position: number;

  constructor(message: string, position: number) {
    super(`${message} (position ${position})`);
    this.name = 'ExpressionSyntaxError';
    this.position = position;
  }
}

export class UnknownContextKeyError extends Error {
  readonly key: string;

  constructor(key: string) {
    super(`Unknown context key: ${key}`);
    this.name = 'UnknownContextKeyError';
    this.key = key;
  }
}

export class ContextResolutionError extends Error {
  readonly commandId: string;
  readonly key: string;

  constructor(commandId: string, key: string) {
    super(`Command "${commandId}" condition references unknown context key: ${key}`);
    this.name = 'ContextResolutionError';
    this.commandId = commandId;
    this.key = key;
  }
}

export class CommandNotFoundError extends Error {
  readonly commandId: string;

  constructor(commandId: string) {
    super(`Command not found: ${commandId}`);
    this.name = 'CommandNotFoundError';
    this.commandId = commandId;
  }
}

export class DuplicateCommandError extends Error {
  readonly commandId: string;

  constructor(commandId: string) {
    super(`Command already registered: ${commandId}`);
    this.name = 'DuplicateCommandError';
    this.commandId = commandId;
  }
}

export class CommandUnavailableError extends Error {
  readonly commandId: string;

  constructor(commandId: string) {
    super(`Command is not available in the current context: ${commandId}`);
    this.name = 'CommandUnavailableError';
    this.commandId = commandId;
  }
}

export class StaleContextError extends Error {
  readonly expectedVersion: number;
  readonly actualVersion: number;

  constructor(expectedVersion: number, actualVersion: number) {
    super(
      `Context changed since listing (expected version ${expectedVersion}, actual ${actualVersion}); re-list commands before executing`,
    );
    this.name = 'StaleContextError';
    this.expectedVersion = expectedVersion;
    this.actualVersion = actualVersion;
  }
}
