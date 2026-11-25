import { createHash } from "node:crypto";
import type { ConstructorDescriptor } from "./types.ts";

/**
 * Deterministic serialization. Object keys are sorted recursively so the same
 * logical document always hashes identically regardless of insertion order.
 */
export function canonicalJson(value: unknown): string {
  return JSON.stringify(toJsonSafe(value));
}

function toJsonSafe(value: unknown): unknown {
  if (value instanceof Map) {
    const entries = [...value.entries()].sort(([a], [b]) =>
      compareStrings(String(a), String(b)),
    );
    return { __map__: entries.map(([k, v]) => [k, toJsonSafe(v)]) };
  }
  if (Array.isArray(value)) {
    return value.map(toJsonSafe);
  }
  if (value !== null && typeof value === "object") {
    const source = value as Record<string, unknown>;
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(source).sort()) {
      out[key] = toJsonSafe(source[key]);
    }
    return out;
  }
  return value;
}

export function sha256Hex(value: unknown): string {
  return createHash("sha256").update(canonicalJson(value)).digest("hex");
}

/** Short deterministic id derived from structural content. */
export function structuralId(prefix: string, content: unknown): string {
  return `${prefix}_${sha256Hex(content).slice(0, 16)}`;
}

export function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function ctorId(descriptor: ConstructorDescriptor): string {
  if (descriptor.array) {
    return `array:${descriptor.arrayRank ?? 1}`;
  }
  return structuralId("ctor", {
    k: descriptor.assemblyPublicKey,
    v: descriptor.assemblyVersion,
    n: descriptor.typeName,
    a: descriptor.arity,
    variance: descriptor.variances,
  });
}

/**
 * Identity of a constructor as observed in a message type graph. Messages
 * carry no variance annotation, so variance is intentionally excluded; the
 * certified catalog supplies variance during admission.
 */
export function graphCtorId(input: {
  assemblyPublicKey: string;
  assemblyVersion: string;
  typeName: string;
  arity: number;
}): string {
  return structuralId("ctor", {
    k: input.assemblyPublicKey,
    v: input.assemblyVersion,
    n: input.typeName,
    a: input.arity,
  });
}

export function ctorKey(
  assemblyPublicKey: string,
  typeName: string,
): string {
  return `${assemblyPublicKey}#${typeName}`;
}

export function arrayDescriptor(rank: number): ConstructorDescriptor {
  return {
    assemblyPublicKey: "",
    assemblyVersion: "",
    typeName: `array:${rank}`,
    arity: 1,
    variances: ["out"],
    array: true,
    arrayRank: rank,
  };
}
