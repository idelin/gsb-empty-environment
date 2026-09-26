/**
 * Presence semantics (contract):
 *   A field is PRESENT iff it is an OWN property of the instance object.
 *   The value is irrelevant: `false`, `null`, `0`, `''` and even an own
 *   `undefined` all count as present. Presence != truthiness, and
 *   presence != (value !== null).
 */
export function isPresent(instance: unknown, field: string): boolean {
  return (
    typeof instance === 'object' &&
    instance !== null &&
    Object.prototype.hasOwnProperty.call(instance, field)
  );
}

export function getValue(instance: unknown, field: string): unknown {
  return (instance as Record<string, unknown>)[field];
}
