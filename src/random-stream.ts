/**
 * tree-random-stream — deterministic, hierarchical random streams.
 *
 * Design contract:
 * - Streams form a tree: each node derives children by test name.
 * - Child streams depend ONLY on (ancestor path, own name). Adding or
 *   removing sibling tests never changes existing streams; renaming a
 *   test changes its stream (and its subtree).
 * - Names are canonically encoded (NFC, length-prefixed UTF-8); names
 *   with ambiguous encodings are rejected.
 * - The generator is a pure function of (seed, index); batched and
 *   one-shot generation are equivalent by construction.
 * - No host randomness is ever used; output depends only on the seed.
 */

const MASK64 = 0xffff_ffff_ffff_ffffn;
const GOLDEN_GAMMA = 0x9e37_79b9_7f4a_7c15n; // splitmix64 increment
const FNV_OFFSET = 0xcbf2_9ce4_8422_2325n;
const FNV_PRIME = 0x0000_0100_0000_01b3n;
const TWO_POW_53 = 9007199254740992; // 2**53
const TWO_POW_64 = 0x1_0000_0000_0000_0000n;

/** Stafford mix13 finalizer (splitmix64 output function). */
function mix64(z: bigint): bigint {
  z = ((z ^ (z >> 30n)) * 0xbf58_476d_1ce4_e5b9n) & MASK64;
  z = ((z ^ (z >> 27n)) * 0x94d0_49bb_1331_11ebn) & MASK64;
  return (z ^ (z >> 31n)) & MASK64;
}

function fnv1a64(bytes: Uint8Array): bigint {
  let hash = FNV_OFFSET;
  for (const byte of bytes) {
    hash ^= BigInt(byte);
    hash = (hash * FNV_PRIME) & MASK64;
  }
  return hash;
}

const encoder = new TextEncoder();

/** Characters that would make a flattened path encoding ambiguous. */
const FORBIDDEN_NAME_CHARS = /[\u0000-\u001f\u007f\/\\\\]/;

/**
 * Canonical encoding of a single path segment:
 *   NFC-normalized UTF-8 bytes, prefixed with their decimal byte length
 *   and a colon: `<byteLength>:<utf8-bytes>`.
 *
 * Length-prefixing makes concatenated segment encodings injective, so
 * distinct name sequences can never collide.
 *
 * Rejects ambiguous names:
 * - empty names (no canonical position in the tree);
 * - names not in Unicode NFC (multiple code-point spellings of the
 *   same visible text would silently fork the stream tree);
 * - names containing control characters, `/` or `\` (they would be
 *   indistinguishable from path separators when flattened).
 */
export function encodeName(name: string): Uint8Array {
  if (typeof name !== "string" || name.length === 0) {
    throw new TypeError("stream name must be a non-empty string");
  }
  if (name !== name.normalize("NFC")) {
    throw new RangeError(
      `stream name is not in Unicode NFC (ambiguous encoding): ${JSON.stringify(name)}`,
    );
  }
  if (FORBIDDEN_NAME_CHARS.test(name)) {
    throw new RangeError(
      `stream name contains a forbidden character (control char, '/' or '\\\\'): ${JSON.stringify(name)}`,
    );
  }
  const utf8 = encoder.encode(name);
  const prefix = encoder.encode(`${utf8.length}:`);
  const out = new Uint8Array(prefix.length + utf8.length);
  out.set(prefix, 0);
  out.set(utf8, prefix.length);
  return out;
}

/** Domain-separated seed derivation from the canonical name encoding. */
function deriveChildSeed(parentSeed: bigint, encodedName: Uint8Array): bigint {
  return mix64(parentSeed ^ fnv1a64(encodedName));
}

export type SeedInput = bigint | number | string;

function toSeed(input: SeedInput): bigint {
  if (typeof input === "bigint") {
    return input & MASK64;
  }
  if (typeof input === "number") {
    if (!Number.isSafeInteger(input)) {
      throw new TypeError("numeric seed must be a safe integer");
    }
    return BigInt(input) & MASK64;
  }
  if (typeof input === "string") {
    // Domain-separated so a string seed can never collide with a child
    // derivation, which always hashes `<len>:`-prefixed encodings.
    return mix64(fnv1a64(encoder.encode(`seed:${input.length}:${input}`)));
  }
  throw new TypeError("seed must be a bigint, safe-integer number, or string");
}

export class RandomStream {
  readonly #seed: bigint;
  #counter: bigint = 0n;

  private constructor(seed: bigint) {
    this.#seed = seed;
  }

  /** Root stream from an explicit seed. Never uses host randomness. */
  static fromSeed(seed: SeedInput): RandomStream {
    return new RandomStream(toSeed(seed));
  }

  /**
   * Derive an independent child stream by name. Pure in the parent's
   * state: calling this before or after drawing from the parent, or
   * before or after deriving siblings, yields the identical stream.
   */
  child(name: string): RandomStream {
    return new RandomStream(deriveChildSeed(this.#seed, encodeName(name)));
  }

  /** Number of 64-bit values drawn so far. */
  get position(): bigint {
    return this.#counter;
  }

  /** Next 64-bit unsigned value of the stream. */
  nextUint64(): bigint {
    this.#counter += 1n;
    const z = (this.#seed + this.#counter * GOLDEN_GAMMA) & MASK64;
    return mix64(z);
  }

  /** Next 32-bit unsigned integer. */
  nextUint32(): number {
    return Number(this.nextUint64() >> 32n);
  }

  /** Next float in [0, 1) with 53 bits of precision. */
  nextFloat(): number {
    return Number(this.nextUint64() >> 11n) / TWO_POW_53;
  }

  /**
   * Next integer in [minInclusive, maxExclusive) via rejection sampling
   * (no modulo bias). Range must be a positive safe integer.
   */
  nextInt(minInclusive: number, maxExclusive: number): number {
    if (
      !Number.isSafeInteger(minInclusive) ||
      !Number.isSafeInteger(maxExclusive) ||
      maxExclusive <= minInclusive
    ) {
      throw new RangeError("require safe integers with min < max");
    }
    const range = BigInt(maxExclusive - minInclusive);
    const limit = (TWO_POW_64 / range) * range;
    let x: bigint;
    do {
      x = this.nextUint64();
    } while (x >= limit);
    return minInclusive + Number(x % range);
  }

  /**
   * Batched generation: exactly equivalent to calling nextFloat()
   * `count` times, because output is a pure function of (seed, index).
   */
  take(count: number): number[] {
    if (!Number.isSafeInteger(count) || count < 0) {
      throw new RangeError("count must be a non-negative safe integer");
    }
    const out = new Array<number>(count);
    for (let i = 0; i < count; i++) {
      out[i] = this.nextFloat();
    }
    return out;
  }
}
