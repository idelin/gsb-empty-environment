export class RetentionPolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RetentionPolicyError";
  }
}

export class RetentionExtensionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RetentionExtensionError";
  }
}

export class ArchiveConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ArchiveConflictError";
  }
}
