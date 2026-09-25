import type { ErrorCode } from "./types.ts";

export class PseudonymizationError extends Error {
  readonly code: ErrorCode;
  readonly path: string;
  constructor(code: ErrorCode, path: string, message: string) {
    super(message);
    this.name = "PseudonymizationError";
    this.code = code;
    this.path = path;
  }
}
