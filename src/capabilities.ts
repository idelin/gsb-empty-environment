import { CapabilityError } from "./errors.ts";

const CAPABILITY_NAMES = [
  "read",
  "write",
  "invoke",
  "construct",
  "enumerate",
] as const;

export type Capability = (typeof CAPABILITY_NAMES)[number];
export const CAPABILITIES: readonly Capability[] = Object.freeze([
  ...CAPABILITY_NAMES,
]);
export type CapabilityMask = number;

export const ALL_CAPABILITY_MASK = (1 << CAPABILITIES.length) - 1;

const BIT: Record<Capability, number> = {
  read: 1 << 0,
  write: 1 << 1,
  invoke: 1 << 2,
  construct: 1 << 3,
  enumerate: 1 << 4,
};

const BY_NAME = new Map<string, Capability>(
  CAPABILITIES.map((capability) => [capability, capability]),
);

export type CapabilityInput =
  | Capability
  | CapabilityMask
  | CapabilitySet
  | Iterable<Capability>
  | ReadonlySet<Capability>
  | null
  | undefined;

/**
 * A compact, immutable set of the five attenuated capabilities.
 */
export class CapabilitySet implements Iterable<Capability> {
  readonly mask: number;

  constructor(input: CapabilityInput = ALL_CAPABILITY_MASK) {
    this.mask = toMask(input);
    Object.freeze(this);
  }

  static readonly ALL = new CapabilitySet(ALL_CAPABILITY_MASK);
  static readonly NONE = new CapabilitySet(0);

  has(capability: Capability): boolean {
    return (this.mask & BIT[capability]) !== 0;
  }

  intersect(other: CapabilityInput): CapabilitySet {
    return new CapabilitySet(this.mask & toMask(other));
  }

  without(capability: CapabilityInput): CapabilitySet {
    return new CapabilitySet(this.mask & ~toMask(capability));
  }

  isSubsetOf(other: CapabilityInput): boolean {
    const otherMask = toMask(other);
    return (this.mask & otherMask) === this.mask;
  }

  isEmpty(): boolean {
    return this.mask === 0;
  }

  toArray(): Capability[] {
    return maskToCapabilities(this.mask);
  }

  toJSON(): Capability[] {
    return this.toArray();
  }

  [Symbol.iterator](): Iterator<Capability> {
    return this.toArray()[Symbol.iterator]();
  }

  toString(): string {
    return this.toArray().join("+") || "none";
  }
}

export function toMask(input: CapabilityInput): number {
  if (input == null) return ALL_CAPABILITY_MASK;
  if (typeof input === "number") {
    if (!Number.isInteger(input) || input < 0 || input > ALL_CAPABILITY_MASK) {
      throw new CapabilityError(`Invalid capability mask: ${String(input)}`);
    }
    return input;
  }
  if (input instanceof CapabilitySet) return input.mask;
  if (typeof input === "string") {
    const capability = BY_NAME.get(input);
    if (capability === undefined) {
      throw new CapabilityError(`Unknown capability: ${input}`, input);
    }
    return BIT[capability];
  }
  if (typeof (input as Iterable<Capability>)[Symbol.iterator] === "function") {
    let mask = 0;
    for (const capability of input as Iterable<Capability>) {
      const normalized = BY_NAME.get(capability);
      if (normalized === undefined) {
        throw new CapabilityError(`Unknown capability: ${String(capability)}`, String(capability));
      }
      mask |= BIT[normalized];
    }
    return mask;
  }
  throw new CapabilityError(`Invalid capability input: ${String(input)}`);
}

export function maskToCapabilities(mask: number): Capability[] {
  const result: Capability[] = [];
  for (const capability of CAPABILITIES) {
    if ((mask & BIT[capability]) !== 0) result.push(capability);
  }
  return result;
}

export function hasCapability(mask: number, capability: Capability): boolean {
  return (mask & BIT[capability]) !== 0;
}

export function requireCapability(
  mask: number,
  capability: Capability,
  operation: string,
): void {
  if (!hasCapability(mask, capability)) {
    throw new CapabilityError(
      `Capability "${capability}" is required for ${operation}`,
      capability,
    );
  }
}

export function intersectMasks(a: number, b: number): number {
  return a & b;
}

export function removeMask(mask: number, remove: number): number {
  return mask & ~remove;
}

export function capabilityEquals(a: CapabilityInput, b: CapabilityInput): boolean {
  return toMask(a) === toMask(b);
}

export function capabilityMaskNames(mask: number): string {
  return maskToCapabilities(mask).join("+") || "none";
}
