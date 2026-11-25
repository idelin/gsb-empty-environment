import { createHash, timingSafeEqual } from "node:crypto";

export const SCHEMA = {
  frozen: "ps-migration/Frozen/v1",
  template: "ps-migration/Template/v1",
  encoderTable: "ps-migration/EncoderTable/v1",
  receipt: "ps-migration/PrefixReceipt/v1",
  authenticatedReceipt: "ps-migration/AuthenticatedReceipt/v1",
  stackSummary: "ps-migration/StackSummary/v1",
  certificate: "ps-migration/MigrationCertificate/v1",
} as const;

export type Json =
  | null
  | string
  | number
  | boolean
  | Json[]
  | { [key: string]: Json | undefined };

export type Verdict = "safe" | "quarantined" | "denied";

export type FindingSeverity = "error" | "warning" | "info";

export interface Finding {
  code: string;
  severity: FindingSeverity;
  message: string;
  evidence?: Json;
}

export class MigrationInputError extends Error {
  readonly code: string;
  readonly evidence?: Json;

  constructor(code: string, message: string, evidence?: Json) {
    super(message);
    this.name = "MigrationInputError";
    this.code = code;
    this.evidence = evidence;
  }
}

export function canonicalJson(value: unknown): string {
  return JSON.stringify(sortValue(value));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") {
    if (value instanceof Uint8Array) {
      return { __type: "Uint8Array", sha256: sha256Hex(value) };
    }
    const record = value as Record<string, unknown>;
    return Object.keys(record)
      .sort()
      .reduce<Record<string, unknown>>((acc, key) => {
        const item = record[key];
        if (item !== undefined) acc[key] = sortValue(item);
        return acc;
      }, {});
  }
  return value;
}

export function sha256Hex(data: string | Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

export function hashCanonical(value: unknown): string {
  return sha256Hex(canonicalJson(value));
}

export function safeEqualHex(a: string, b: string): boolean {
  if (!/^[0-9a-f]{64}$/i.test(a) || !/^[0-9a-f]{64}$/i.test(b)) return false;
  const ab = Buffer.from(a.toLowerCase(), "hex");
  const bb = Buffer.from(b.toLowerCase(), "hex");
  if (ab.length !== bb.length) return false;
  return timingSafeEqual(ab, bb);
}

export function safeEqualString(a: string, b: string): boolean {
  const ab = Buffer.from(a);
  const bb = Buffer.from(b);
  if (ab.length !== bb.length) return false;
  return timingSafeEqual(ab, bb);
}

export interface Frozen<T> {
  readonly schema: typeof SCHEMA.frozen;
  readonly sha256: string;
  readonly value: T;
}

export function freezeDocument<T extends object>(value: T): Frozen<T> {
  deepFreeze(value);
  return Object.freeze({
    schema: SCHEMA.frozen,
    sha256: hashCanonical(value),
    value: Object.freeze(value),
  });
}

function deepFreeze(value: unknown): void {
  if (!value || typeof value !== "object" || Object.isFrozen(value)) return;
  Object.freeze(value);
  for (const key of Reflect.ownKeys(value)) {
    const child = (value as Record<PropertyKey, unknown>)[key];
    if (child && typeof child === "object" && !Object.isFrozen(child)) deepFreeze(child);
  }
}

export function verifyFrozen<T>(doc: unknown): doc is Frozen<T> {
  if (!doc || typeof doc !== "object") return false;
  const frozen = doc as Partial<Frozen<T>>;
  return (
    frozen.schema === SCHEMA.frozen &&
    typeof frozen.sha256 === "string" &&
    !!frozen.value &&
    hashCanonical(frozen.value) === frozen.sha256
  );
}

export function requireFrozen<T>(doc: Frozen<T>, label: string): Finding[] {
  if (!verifyFrozen<T>(doc)) {
    return [
      {
        code: "FROZEN_DOCUMENT_INVALID",
        severity: "error",
        message: `${label} is not a hash-consistent frozen document`,
      },
    ];
  }
  return [];
}

export function mergeFindings(...groups: Finding[][]): Finding[] {
  return groups.flat();
}

export function worstVerdict(findings: Finding[]): Verdict | undefined {
  if (findings.some((f) => f.severity === "error")) return "denied";
  if (findings.some((f) => f.severity === "warning")) return "quarantined";
  return undefined;
}

export function deduplicateFindings(findings: Finding[]): Finding[] {
  const seen = new Set<string>();
  const result: Finding[] = [];
  for (const finding of findings) {
    const key = `${finding.code}:${finding.message}:${canonicalJson(finding.evidence ?? null)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(finding);
  }
  return result;
}
