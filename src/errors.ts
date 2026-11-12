export class ActivationError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'ActivationError';
  }
}

export class CrossDocumentTransferError extends ActivationError {
  constructor(tokenDocumentId: string, callerDocumentId: string) {
    super(
      `activation token bound to document "${tokenDocumentId}" cannot be consumed from document "${callerDocumentId}"`,
    );
    this.name = 'CrossDocumentTransferError';
  }
}

export class TokenUnavailableError extends ActivationError {
  readonly reason: string;

  constructor(reason: string, detail: string) {
    super(`${reason}: ${detail}`);
    this.name = 'TokenUnavailableError';
    this.reason = reason;
  }
}
