export class BatchReleasedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'BatchReleasedError';
  }
}

export class StaleViewError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'StaleViewError';
  }
}

export class LoadCancelledError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'LoadCancelledError';
  }
}

export class PoolExhaustedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PoolExhaustedError';
  }
}

export class UnknownColumnError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnknownColumnError';
  }
}
