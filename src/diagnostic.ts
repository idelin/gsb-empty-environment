/**
 * Diagnostic construction. All diagnostics pass through this helper so the
 * `ContextDiagnostic` contract can never carry an `undefined` member.
 */

import { ContextResolutionError } from "./types.ts";
import type {
  ContentId,
  ContextDiagnostic,
  ErrorCode,
} from "./types.ts";

export interface DiagnosticParts {
  readonly code: ErrorCode;
  readonly message: string;
  readonly offendingTerm?: string;
  readonly offendingCid?: string;
  readonly importPath?: readonly ContentId[];
  readonly depth?: number;
  readonly detail?: string;
}

export function raise(parts: DiagnosticParts): never {
  const diagnostic: ContextDiagnostic = {
    code: parts.code,
    message: parts.message,
    offendingTerm: parts.offendingTerm ?? "",
    offendingCid: parts.offendingCid ?? "",
    importPath: parts.importPath ? [...parts.importPath] : [],
    depth: parts.depth ?? 0,
    detail: parts.detail ?? "",
  };
  throw new ContextResolutionError(diagnostic);
}

/** Assert a serialised diagnostic round-trips without undefined members. */
export function assertDiagnosticComplete(diagnostic: ContextDiagnostic): void {
  const keys = [
    "code",
    "message",
    "offendingTerm",
    "offendingCid",
    "importPath",
    "depth",
    "detail",
  ] as const;
  for (const key of keys) {
    const value = diagnostic[key];
    if (value === undefined) {
      throw new Error(`Diagnostic missing required field ${key}`);
    }
  }
  if (!Array.isArray(diagnostic.importPath)) {
    throw new Error("Diagnostic importPath must always be an array");
  }
}

/** JSON.stringify replacer that fails loudly rather than emitting undefined. */
export function safeDiagnosticJson(diagnostic: ContextDiagnostic): string {
  assertDiagnosticComplete(diagnostic);
  return JSON.stringify(diagnostic);
}
