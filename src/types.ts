/**
 * Declarative cross-origin exposure policy.
 *
 * - `true`  : the object is fully exposed (all own properties reachable),
 *             still membrane-wrapped and therefore revocable.
 * - `false` : nothing on the object is exposed.
 * - object  : only the listed keys are exposed; a nested object describes the
 *             policy for the object reachable through that key.
 *
 * The meta-key `$return` on a method entry declares the surface of objects
 * that method returns. Without it, objects returned by an exposed method are
 * fully restricted (no member access) even though the method itself is callable.
 *
 * Example:
 * ```ts
 * { reader: { read: true }, title: true }
 * ```
 */
export type ExposeSchema = boolean | { [key: string]: ExposeSchema };

export interface WrapOptions {
  /**
   * `true` produces a cross-origin membrane that only exposes the operations
   * declared in `expose`. Defaults to `false` (same-origin, full access).
   */
  crossOrigin?: boolean;
  /**
   * Policy applied to cross-origin wrappers. Defaults to `false`, i.e. a
   * cross-origin wrapper exposes nothing unless explicitly declared.
   * Ignored for same-origin wrappers.
   */
  expose?: ExposeSchema;
}
