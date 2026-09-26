/**
 * Canonical, injective encoding of composite keys.
 *
 * Every component is encoded as `<tag><charLength>:<payload>` and components
 * are joined with "|". Because each component is self-delimiting (fixed tag +
 * explicit length), the concatenation is uniquely decodable, which makes the
 * whole encoding injective:
 *
 *   - separators inside values ("|", ":", ...) can never cause collisions,
 *     because the payload length - not the separator - delimits the value;
 *   - different types never collide: 1 (N), "1" (S), true (B), null (Z/z)
 *     and a missing field (U) all carry distinct tags;
 *   - numbers are canonicalized ("1" vs "1.0" vs -0 collapse to one form).
 *
 * Tags:
 *   U  missing/undefined field        payload: ""
 *   Z  null (nullsEqual: true)        payload: ""
 *   z  null (nullsEqual: false)       payload: "#<recordIndex>" (unique per record)
 *   S  string                         payload: raw string
 *   N  number                         payload: canonical number string
 *   B  boolean                        payload: "true" | "false"
 *   J  other JSON values (out of the promised key subset, encoded structurally)
 */

export interface EncodeOptions {
  nullsEqual: boolean;
  /** Used to make nulls distinct from each other when nullsEqual is false. */
  recordIndex: number;
}

export function encodeCompositeKey(values: readonly unknown[], options: EncodeOptions): string {
  return values.map((value) => encodeComponent(value, options)).join("|");
}

function encodeComponent(value: unknown, options: EncodeOptions): string {
  if (value === undefined) return "U0:";
  if (value === null) {
    if (options.nullsEqual) return "Z0:";
    const token = `#${options.recordIndex}`;
    return `z${token.length}:${token}`;
  }
  switch (typeof value) {
    case "string":
      return `S${value.length}:${value}`;
    case "boolean": {
      const payload = value ? "true" : "false";
      return `B${payload.length}:${payload}`;
    }
    case "number": {
      const payload = canonicalNumber(value);
      return `N${payload.length}:${payload}`;
    }
    default: {
      const payload = JSON.stringify(value) ?? "";
      return `J${payload.length}:${payload}`;
    }
  }
}

function canonicalNumber(value: number): string {
  if (Number.isNaN(value)) return "NaN";
  if (value === Infinity) return "Infinity";
  if (value === -Infinity) return "-Infinity";
  if (value === 0) return "0"; // normalizes -0 to 0
  return String(value);
}

/** Inverse of encodeCompositeKey; a null encoded with nullsEqual:false decodes to null. */
export function decodeCompositeKey(encoded: string): unknown[] {
  const values: unknown[] = [];
  let pos = 0;
  while (pos < encoded.length) {
    const tag = encoded[pos];
    const colon = encoded.indexOf(":", pos);
    if (colon === -1) throw new Error(`malformed encoded key at offset ${pos}`);
    const length = Number(encoded.slice(pos + 1, colon));
    if (!Number.isInteger(length) || length < 0) {
      throw new Error(`malformed length in encoded key at offset ${pos}`);
    }
    const payload = encoded.slice(colon + 1, colon + 1 + length);
    if (payload.length !== length) throw new Error(`truncated encoded key at offset ${pos}`);
    switch (tag) {
      case "U":
        values.push(undefined);
        break;
      case "Z":
      case "z":
        values.push(null);
        break;
      case "S":
        values.push(payload);
        break;
      case "B":
        values.push(payload === "true");
        break;
      case "N":
        values.push(decodeNumber(payload));
        break;
      case "J":
        values.push(JSON.parse(payload));
        break;
      default:
        throw new Error(`unknown tag "${tag}" in encoded key at offset ${pos}`);
    }
    pos = colon + 1 + length;
    if (pos < encoded.length) {
      if (encoded[pos] !== "|") throw new Error(`missing component separator at offset ${pos}`);
      pos += 1;
    }
  }
  return values;
}

function decodeNumber(payload: string): number {
  if (payload === "NaN") return NaN;
  if (payload === "Infinity") return Infinity;
  if (payload === "-Infinity") return -Infinity;
  return Number(payload);
}
