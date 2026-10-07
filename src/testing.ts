import type { FieldValues, SubmissionResult, Submitter } from "./form-store.ts";

export interface ControlledRequest {
  /** Submission id handed to the submitter. */
  id: number;
  /** Frozen snapshot as sent to the server. */
  snapshot: Readonly<FieldValues>;
  signal: AbortSignal;
  resolve(result: SubmissionResult): void;
  reject(error: unknown): void;
}

/**
 * A submitter whose responses are resolved manually, for deterministic
 * tests of in-flight behavior (duplicate submit, cancel, deleted fields).
 */
export function createControllableSubmitter(): {
  submitter: Submitter;
  requests: ControlledRequest[];
} {
  const requests: ControlledRequest[] = [];
  const submitter: Submitter = (snapshot, context) =>
    new Promise<SubmissionResult>((resolve, reject) => {
      requests.push({
        id: context.submissionId,
        snapshot,
        signal: context.signal,
        resolve,
        reject,
      });
    });
  return { submitter, requests };
}
