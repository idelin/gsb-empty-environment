export class TreeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class NotFoundError extends TreeError {}
export class CycleError extends TreeError {}
export class AliasConflictError extends TreeError {}
export class DeletedTargetError extends TreeError {}
export class RootEscapeError extends TreeError {}
export class ReadOnlyError extends TreeError {}
export class MoveValidationError extends TreeError {}
export class StaleConfigError extends TreeError {}
