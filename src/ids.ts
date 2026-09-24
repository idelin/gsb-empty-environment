import { randomUUID } from "node:crypto";

/**
 * Every insert operation carries an operation identity. Two inserts of the
 * same text are still distinct edits because their ids differ.
 */
export type IdGen = () => string;

/** Default id generator: cryptographically unique per call. */
export const randomOpId: IdGen = () => `op_${randomUUID()}`;

/** Deterministic id generator for tests and reproducible demos. */
export function createIdGen(prefix = "op"): IdGen {
  let counter = 0;
  return () => `${prefix}_${counter++}`;
}
