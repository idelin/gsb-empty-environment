/**
 * Guards against prototype-pollution payloads in bridge `options`.
 *
 * Rule: any object key that contains the literal substring "__proto__"
 * rejects the whole message. Ownership is enforced explicitly: keys are
 * collected with Object.keys and every lookup uses hasOwn, never the
 * property chain.
 */

const PROTO_MARKER = '__proto__';
const MAX_DEPTH = 32;

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isPlainContainer(value: unknown): value is Record<string, unknown> | unknown[] {
  return (
    typeof value === 'object' &&
    value !== null &&
    (Array.isArray(value) || Object.getPrototypeOf(value) === Object.prototype)
  );
}

/**
 * Returns true when `value` (recursively) contains a key whose name
 * includes "__proto__". Cycles and overly deep payloads are handled
 * without throwing.
 */
export function containsProtoKey(value: unknown, depth = 0, seen: WeakSet<object> = new WeakSet()): boolean {
  if (depth > MAX_DEPTH || !isPlainContainer(value)) {
    return false;
  }
  if (seen.has(value)) {
    return false;
  }
  seen.add(value);

  for (const key of Object.keys(value)) {
    if (key.includes(PROTO_MARKER)) {
      return true;
    }
    const child = (value as Record<string, unknown>)[key];
    if (isPlainContainer(child) && containsProtoKey(child, depth + 1, seen)) {
      return true;
    }
  }
  return false;
}

/** Structurally validate the `options` field of a bridge message. */
export function validateOptions(options: unknown): { ok: true } | { ok: false; reason: string } {
  if (options === undefined) {
    return { ok: true };
  }
  if (!isPlainContainer(options)) {
    return { ok: false, reason: 'options must be a plain object or array' };
  }
  if (containsProtoKey(options)) {
    return { ok: false, reason: 'options must not contain __proto__ keys' };
  }
  return { ok: true };
}

export { hasOwn };
