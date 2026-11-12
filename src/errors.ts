/**
 * All policy / release failures are typed so the release interface can map
 * them onto the correct gRPC / REST error codes without parsing messages.
 */
export class PolicyError extends Error {
  override readonly name = "PolicyError";
}

export class InvalidPolicyError extends PolicyError {
  override readonly name = "InvalidPolicyError";
  readonly tracePath: ReadonlyArray<string | number>;
  constructor(
    message: string,
    tracePath: ReadonlyArray<string | number> = [],
  ) {
    super(tracePath.length ? `${message} (at ${tracePath.join(".")})` : message);
    this.tracePath = tracePath;
  }
}

export class TokenVerificationError extends PolicyError {
  override readonly name = "TokenVerificationError";
}

export class PolicyNotSatisfiedError extends PolicyError {
  override readonly name = "PolicyNotSatisfiedError";
  readonly trace: ReadonlyArray<unknown>;
  constructor(
    message: string,
    trace: ReadonlyArray<unknown>,
  ) {
    super(message);
    this.trace = trace;
  }
}

export class RuntimeDataBindingError extends PolicyError {
  override readonly name = "RuntimeDataBindingError";
}

export class KeyWrappingError extends PolicyError {
  override readonly name = "KeyWrappingError";
}
