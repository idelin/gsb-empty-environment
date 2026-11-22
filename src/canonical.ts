/**
 * Deterministic JSON, SHA-256 and HMAC-SHA-256.
 *
 * The implementation is dependency-free so the same code runs in a browser
 * worker (through the package's bundler) and in Node's type-stripping test
 * runner.
 */

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

function rotr(value: number, count: number): number {
  return ((value >>> count) | (value << (32 - count))) >>> 0;
}

export function sha256(input: Uint8Array): Uint8Array {
  const length = input.length;
  const paddedLength = (((length + 9 + 63) >> 6) << 6);
  const padded = new Uint8Array(paddedLength);
  padded.set(input);
  padded[length] = 0x80;

  const bitLength = length * 8;
  const high = Math.floor(bitLength / 0x100000000);
  const low = bitLength >>> 0;
  padded[paddedLength - 8] = (high >>> 24) & 0xff;
  padded[paddedLength - 7] = (high >>> 16) & 0xff;
  padded[paddedLength - 6] = (high >>> 8) & 0xff;
  padded[paddedLength - 5] = high & 0xff;
  padded[paddedLength - 4] = (low >>> 24) & 0xff;
  padded[paddedLength - 3] = (low >>> 16) & 0xff;
  padded[paddedLength - 2] = (low >>> 8) & 0xff;
  padded[paddedLength - 1] = low & 0xff;

  let h0 = 0x6a09e667;
  let h1 = 0xbb67ae85;
  let h2 = 0x3c6ef372;
  let h3 = 0xa54ff53a;
  let h4 = 0x510e527f;
  let h5 = 0x9b05688c;
  let h6 = 0x1f83d9ab;
  let h7 = 0x5be0cd19;

  const w = new Uint32Array(64);
  for (let offset = 0; offset < paddedLength; offset += 64) {
    for (let i = 0; i < 16; i++) {
      const p = offset + i * 4;
      w[i] =
        ((padded[p] << 24) |
          (padded[p + 1] << 16) |
          (padded[p + 2] << 8) |
          padded[p + 3]) >>>
        0;
    }
    for (let i = 16; i < 64; i++) {
      const x = w[i - 15];
      const y = w[i - 2];
      const s0 = rotr(x, 7) ^ rotr(x, 18) ^ (x >>> 3);
      const s1 = rotr(y, 17) ^ rotr(y, 19) ^ (y >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }

    let a = h0;
    let b = h1;
    let c = h2;
    let d = h3;
    let e = h4;
    let f = h5;
    let g = h6;
    let h = h7;

    for (let i = 0; i < 64; i++) {
      const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const choose = (e & f) ^ (~e & g);
      const temp1 = (h + s1 + choose + K[i] + w[i]) >>> 0;
      const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const majority = (a & b) ^ (a & c) ^ (b & c);
      const temp2 = (s0 + majority) >>> 0;

      h = g;
      g = f;
      f = e;
      e = (d + temp1) >>> 0;
      d = c;
      c = b;
      b = a;
      a = (temp1 + temp2) >>> 0;
    }

    h0 = (h0 + a) >>> 0;
    h1 = (h1 + b) >>> 0;
    h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0;
    h4 = (h4 + e) >>> 0;
    h5 = (h5 + f) >>> 0;
    h6 = (h6 + g) >>> 0;
    h7 = (h7 + h) >>> 0;
  }

  const out = new Uint8Array(32);
  const words = [h0, h1, h2, h3, h4, h5, h6, h7];
  for (let i = 0; i < 8; i++) {
    out[i * 4] = (words[i] >>> 24) & 0xff;
    out[i * 4 + 1] = (words[i] >>> 16) & 0xff;
    out[i * 4 + 2] = (words[i] >>> 8) & 0xff;
    out[i * 4 + 3] = words[i] & 0xff;
  }
  return out;
}

export function utf8(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

export function toHex(bytes: Uint8Array): string {
  let out = "";
  for (const byte of bytes) {
    out += byte.toString(16).padStart(2, "0");
  }
  return out;
}

export function fromHex(text: string): Uint8Array {
  const clean = text.trim().toLowerCase();
  if (clean.length % 2 !== 0 || !/^[0-9a-f]*$/.test(clean)) {
    throw new Error("invalid hex string");
  }
  const out = new Uint8Array(clean.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

const BASE64_ALPHABET =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

export function toBase64(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i];
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : 0;
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : 0;
    out += BASE64_ALPHABET[b0 >>> 2];
    out += BASE64_ALPHABET[((b0 & 0x03) << 4) | (b1 >>> 4)];
    out +=
      i + 1 < bytes.length
        ? BASE64_ALPHABET[((b1 & 0x0f) << 2) | (b2 >>> 6)]
        : "=";
    out += i + 2 < bytes.length ? BASE64_ALPHABET[b2 & 0x3f] : "=";
  }
  return out;
}

export function fromBase64(text: string): Uint8Array {
  let clean = text.replace(/\s+/g, "");
  if (clean.length % 4 === 2) clean += "==";
  else if (clean.length % 4 === 3) clean += "=";
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(clean) || clean.length % 4 !== 0) {
    throw new Error("invalid base64 string");
  }
  const padding = clean.endsWith("==") ? 2 : clean.endsWith("=") ? 1 : 0;
  const out = new Uint8Array((clean.length / 4) * 3 - padding);
  let offset = 0;
  for (let i = 0; i < clean.length; i += 4) {
    const c0 = BASE64_ALPHABET.indexOf(clean[i]);
    const c1 = BASE64_ALPHABET.indexOf(clean[i + 1]);
    const c2 = clean[i + 2] === "=" ? 0 : BASE64_ALPHABET.indexOf(clean[i + 2]);
    const c3 = clean[i + 3] === "=" ? 0 : BASE64_ALPHABET.indexOf(clean[i + 3]);
    if (c0 < 0 || c1 < 0 || c2 < 0 || c3 < 0) {
      throw new Error("invalid base64 string");
    }
    const n = (c0 << 18) | (c1 << 12) | (c2 << 6) | c3;
    if (offset < out.length) out[offset++] = (n >>> 16) & 0xff;
    if (offset < out.length) out[offset++] = (n >>> 8) & 0xff;
    if (offset < out.length) out[offset++] = n & 0xff;
  }
  return out;
}

export function concatBytes(...parts: readonly Uint8Array[]): Uint8Array {
  let total = 0;
  for (const part of parts) total += part.length;
  const out = new Uint8Array(total);
  let offset = 0;
  for (const part of parts) {
    out.set(part, offset);
    offset += part.length;
  }
  return out;
}

export function sha256Hex(input: Uint8Array | string): string {
  return toHex(sha256(typeof input === "string" ? utf8(input) : input));
}

export function hmacSha256(
  key: Uint8Array,
  message: Uint8Array,
): Uint8Array {
  const normalizedKey = key.length > 64 ? sha256(key) : key;
  const block = new Uint8Array(64);
  block.set(normalizedKey);
  const inner = new Uint8Array(64);
  const outer = new Uint8Array(64);
  for (let i = 0; i < 64; i++) {
    inner[i] = block[i] ^ 0x36;
    outer[i] = block[i] ^ 0x5c;
  }
  return sha256(concatBytes(outer, sha256(concatBytes(inner, message))));
}

export function hmacSha256Hex(key: Uint8Array | string, message: string): string {
  const keyBytes = typeof key === "string" ? utf8(key) : key;
  return toHex(hmacSha256(keyBytes, utf8(message)));
}

export function constantTimeEqual(a: Uint8Array, b: Uint8Array): boolean {
  let difference = a.length ^ b.length;
  const length = Math.min(a.length, b.length);
  for (let i = 0; i < length; i++) difference |= a[i] ^ b[i];
  return difference === 0;
}

export function canonicalJson(value: unknown): string {
  return canonicalize(value, new Set());
}

function canonicalize(value: unknown, seen: Set<object>): string {
  if (value === null) return "null";
  if (value === undefined) return "null";
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      throw new Error("canonicalJson cannot encode a non-finite number");
    }
    return JSON.stringify(value);
  }
  if (typeof value === "bigint") return value.toString();
  if (Array.isArray(value)) {
    return `[${value
      .map((item) => canonicalize(item, seen))
      .join(",")}]`;
  }
  if (value instanceof Uint8Array) {
    return `[${Array.from(value).join(",")}]`;
  }
  if (typeof value === "object") {
    const object = value as Record<string, unknown>;
    if (seen.has(object)) {
      throw new Error("canonicalJson cannot encode a cycle");
    }
    seen.add(object);
    const keys = Object.keys(object)
      .filter((key) => object[key] !== undefined)
      .sort();
    const body = keys
      .map((key) => `${JSON.stringify(key)}:${canonicalize(object[key], seen)}`)
      .join(",");
    seen.delete(object);
    return `{${body}}`;
  }
  throw new Error(`canonicalJson cannot encode ${typeof value}`);
}

export function digestValue(value: unknown): string {
  return sha256Hex(canonicalJson(value));
}
