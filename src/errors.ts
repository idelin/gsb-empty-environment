export class IncommensurableError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'IncommensurableError';
  }
}

export class ContradictoryConversionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'ContradictoryConversionError';
  }
}

export class QuantityTypeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'QuantityTypeError';
  }
}
