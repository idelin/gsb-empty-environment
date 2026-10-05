/** Thrown when the node/ordinate arrays are malformed. */
export class InvalidInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InvalidInputError';
  }
}

/** Thrown when the same abscissa appears with two different ordinates. */
export class DuplicateAbscissaError extends Error {
  readonly x: number;
  readonly firstValue: number;
  readonly conflictingValue: number;

  constructor(x: number, firstValue: number, conflictingValue: number) {
    super(
      `Duplicate abscissa x=${x} with conflicting ordinates ` +
        `${firstValue} and ${conflictingValue}`,
    );
    this.name = 'DuplicateAbscissaError';
    this.x = x;
    this.firstValue = firstValue;
    this.conflictingValue = conflictingValue;
  }
}

/** Thrown when `requireMonotone` is set but the ordinates are not monotone. */
export class NotMonotoneError extends Error {
  constructor() {
    super('Ordinates are not monotone and `requireMonotone` is set');
    this.name = 'NotMonotoneError';
  }
}
