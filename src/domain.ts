/**
 * Explicit finite domains. Every attribute a policy may read must be declared
 * here; there is no external identity service to fall back to.
 *
 * An attribute declared with `optional: true` may be absent (unknown), which
 * switches evaluation to a three-valued semantics.
 */

import type { Scalar } from "./conditions.ts";

/** Marker for an absent/unknown attribute value. */
export const UNKNOWN = Symbol("unknown");
export type Unknown = typeof UNKNOWN;

export type Value = Scalar | readonly Scalar[];
export type Assignment = Record<string, Value | Unknown>;

export interface EnumDomain {
  kind: "enum";
  values: readonly Scalar[];
  optional?: boolean;
}

export interface IntDomain {
  kind: "int";
  min: number;
  max: number;
  optional?: boolean;
}

export interface SetDomain {
  kind: "set";
  /** Explicit list of allowed sets (the finite domain of the attribute). */
  sets: readonly (readonly Scalar[])[];
  optional?: boolean;
}

export type AttrDomain = EnumDomain | IntDomain | SetDomain;
export type DomainSpec = Record<string, AttrDomain>;

/** A "box": the unit of interval splitting. */
export type AttrBox =
  | { kind: "int"; lo: number; hi: number }
  | { kind: "enum"; values: readonly Scalar[] }
  | { kind: "set"; sets: readonly (readonly Scalar[])[] };

export type Box = Record<string, AttrBox>;

export function validateDomain(domain: DomainSpec): void {
  for (const [name, d] of Object.entries(domain)) {
    if (d.kind === "int") {
      if (!Number.isInteger(d.min) || !Number.isInteger(d.max)) {
        throw new Error(`domain "${name}": int bounds must be integers`);
      }
    } else if (d.kind === "enum") {
      if (new Set(d.values).size !== d.values.length) {
        throw new Error(`domain "${name}": enum values must be distinct`);
      }
    } else {
      for (const s of d.sets) {
        if (new Set(s).size !== s.length) {
          throw new Error(`domain "${name}": set entries must be distinct`);
        }
      }
    }
  }
}

/** True when at least one attribute has an empty range (no assignments). */
export function domainIsEmpty(domain: DomainSpec, attrs: readonly string[]): boolean {
  return attrs.some((a) => {
    const d = domain[a];
    if (d.kind === "int") return d.min > d.max;
    if (d.kind === "enum") return d.values.length === 0;
    return d.sets.length === 0;
  });
}

export function initialBox(domain: DomainSpec, attrs: readonly string[]): Box {
  const box: Box = {};
  for (const a of attrs) {
    const d = domain[a];
    if (d.kind === "int") box[a] = { kind: "int", lo: d.min, hi: d.max };
    else if (d.kind === "enum") box[a] = { kind: "enum", values: d.values };
    else box[a] = { kind: "set", sets: d.sets };
  }
  return box;
}

function attrSize(b: AttrBox): number {
  if (b.kind === "int") return b.hi - b.lo + 1;
  if (b.kind === "enum") return b.values.length;
  return b.sets.length;
}

export function boxIsSingleton(box: Box, order: readonly string[]): boolean {
  return order.every((a) => attrSize(box[a]) === 1);
}

/**
 * The most significant (first in `order`) attribute that still has more than
 * one possibility. Splitting in this fixed order makes the depth-first search
 * visit assignments in lexicographic order, which is what makes the first
 * counterexample found the minimal one.
 */
export function mostSignificantSplittable(
  box: Box,
  order: readonly string[],
): string | null {
  for (const a of order) {
    if (attrSize(box[a]) > 1) return a;
  }
  return null;
}

/** Split one attribute box into a lower/earlier half and an upper/later half. */
export function splitAttr(b: AttrBox): [AttrBox, AttrBox] {
  if (b.kind === "int") {
    const mid = Math.floor((b.lo + b.hi) / 2);
    return [
      { kind: "int", lo: b.lo, hi: mid },
      { kind: "int", lo: mid + 1, hi: b.hi },
    ];
  }
  if (b.kind === "enum") {
    const half = Math.ceil(b.values.length / 2);
    return [
      { kind: "enum", values: b.values.slice(0, half) },
      { kind: "enum", values: b.values.slice(half) },
    ];
  }
  const half = Math.ceil(b.sets.length / 2);
  return [
    { kind: "set", sets: b.sets.slice(0, half) },
    { kind: "set", sets: b.sets.slice(half) },
  ];
}

/** Component-wise minimal point of a box (ints: lower bound; enums/sets: first listed). */
export function minPoint(box: Box, order: readonly string[]): Assignment {
  const out: Assignment = {};
  for (const a of order) {
    const b = box[a];
    if (b.kind === "int") out[a] = b.lo;
    else if (b.kind === "enum") out[a] = b.values[0];
    else out[a] = [...b.sets[0]];
  }
  return out;
}

export function boxKey(box: Box, order: readonly string[]): string {
  return order
    .map((a) => {
      const b = box[a];
      if (b.kind === "int") return `${a}=[${b.lo},${b.hi}]`;
      if (b.kind === "enum") return `${a}={${b.values.map(String).join(",")}}`;
      return `${a}=<${b.sets.map((s) => s.map(String).join(".")).join(",")}>`;
    })
    .join(";");
}
