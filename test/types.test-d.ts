/**
 * Compile-time checks. Under `tsc --noEmit` the commented error lines must
 * remain rejected; the accepted lines describe the intended usage. This file
 * contains no runtime assertions on purpose.
 */
import type { VerificationFailure, VerifiedSummary } from "../src/index.ts";
import { useSummary } from "../src/index.ts";

declare const sealed: VerifiedSummary;
declare const failure: VerificationFailure;

// Accepted: sealed summaries are the only caller-visible contract handle.
export const accepted: VerifiedSummary = useSummary(sealed);

// @ts-expect-error a failed verification result carries no brand and cannot be consumed
export const rejected: VerifiedSummary = useSummary(failure);

// @ts-expect-error arbitrary plain objects cannot impersonate a verified summary
export const forged: VerifiedSummary = { id: "fake" };

void accepted;
void rejected;
void forged;
