/**
 * Address-bar Unicode display decision package.
 *
 * Entry points:
 *   - decideDisplay / decideForVersion
 *   - verifyDecision / checkAllowedRange
 *   - runExhaustiveChecks
 */

export * from "./types.ts";
export * from "./canonical.ts";
export * from "./unicode.ts";
export * from "./confusables.ts";
export * from "./scripts.ts";
export * from "./suffix.ts";
export * from "./aliases.ts";
export * from "./automaton.ts";
export * from "./tables.ts";
export * from "./decide.ts";
export * from "./checker.ts";
export * from "./exhaustive.ts";
