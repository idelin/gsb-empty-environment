import { isPlainObject } from "./json.ts";
import type { JsonObject } from "./json.ts";

/**
 * Delete sentinel: removes the key/item from the merged result, masking every
 * lower-priority layer. It is NOT the same as JSON `null`, which is an
 * ordinary value ("set this field to null").
 */
export const DELETE: JsonObject = Object.freeze({ $delete: true });

export function isDeleteSentinel(v: unknown): boolean {
  return (
    isPlainObject(v) && Object.keys(v).length === 1 && v.$delete === true
  );
}
