import type { CheckCode } from "./types.ts";

export class DelegationError extends Error {
  readonly code: CheckCode;
  readonly grantId: string | null;

  constructor(code: CheckCode, grantId: string | null, detail: string) {
    super(`${code}${grantId ? ` [${grantId}]` : ""}: ${detail}`);
    this.name = "DelegationError";
    this.code = code;
    this.grantId = grantId;
  }
}
