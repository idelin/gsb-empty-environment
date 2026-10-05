export class PruneError extends Error {
  constructor(message: string) {
    super(message);
    this.name = new.target.name;
  }
}

export class ExpressionError extends PruneError {}

export class UnknownVariableError extends ExpressionError {
  readonly varName: string;

  constructor(varName: string) {
    super(`unknown variable '${varName}'`);
    this.varName = varName;
  }
}

export class DirectiveError extends PruneError {
  readonly line: number;

  constructor(message: string, line: number) {
    super(`line ${line}: ${message}`);
    this.line = line;
  }
}
