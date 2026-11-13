export class ChallengeParseError extends Error {
  override readonly name = "ChallengeParseError";

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
  }
}

export class OriginVerificationError extends Error {
  override readonly name = "OriginVerificationError";

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
  }
}
