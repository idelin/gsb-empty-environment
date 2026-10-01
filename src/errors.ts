export class DivisionByZeroError extends Error {
  constructor(message = 'division by zero') {
    super(message);
    this.name = 'DivisionByZeroError';
  }
}

export class InvalidOperationError extends Error {
  constructor(message = 'invalid operation') {
    super(message);
    this.name = 'InvalidOperationError';
  }
}
