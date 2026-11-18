import assert from "node:assert/strict";
import type { FedCMResult } from "../index.ts";

export function expectOk<T>(result: FedCMResult<T>): T {
  assert.equal(result.ok, true);
  if (!result.ok) throw new Error(result.error.message);
  return result.value;
}

export function expectError<T>(
  result: FedCMResult<T>,
  code: string,
  status?: number,
) {
  assert.equal(result.ok, false);
  if (result.ok) throw new Error("expected validation to fail");
  assert.equal(result.error.code, code);
  if (status !== undefined) assert.equal(result.error.status, status);
  return result.error;
}
