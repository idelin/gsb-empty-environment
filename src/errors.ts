import type { Leak } from "./types.ts";

export class DesensitizationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "DesensitizationError";
  }
}

export class DesensitizationLeakError extends DesensitizationError {
  readonly leaks: Leak[];

  constructor(leaks: Leak[]) {
    const summary = leaks
      .slice(0, 3)
      .map((leak) => `${leak.encoding}@${leak.path ?? `index ${leak.index}`}`)
      .join(", ");
    super(`Refusing to emit a response with sensitive data (${summary})`);
    this.name = "DesensitizationLeakError";
    this.leaks = leaks;
  }
}

export class CircularReferenceError extends DesensitizationError {
  readonly path: string;

  constructor(path: string) {
    super(`Circular reference while serializing at ${path}`);
    this.name = "CircularReferenceError";
    this.path = path;
  }
}

export class AuditDecryptionError extends DesensitizationError {
  constructor(message = "Unable to decrypt audit mapping") {
    super(message);
    this.name = "AuditDecryptionError";
  }
}
