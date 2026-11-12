import { SecurityError } from './errors.ts';
import { Membrane } from './membrane.ts';
import type { ExposeSchema, WrapOptions } from './types.ts';

/**
 * Trusted host handle. The capability identities (WeakMaps) are encapsulated
 * inside the private {@link Membrane}; this class only hands back membrane
 * proxies and never exposes the membrane internals or original targets.
 */
export class Host {
  readonly #membrane = new Membrane();

  /**
   * Wrap a trusted object before handing it to a script.
   *
   * @param target   trusted object/function the script must not reach directly
   * @param document opaque token identifying the owning document
   * @param options  same-origin (full) vs cross-origin (allow-list) wrapping
   */
  wrap<T extends object>(target: T, document: object, options?: WrapOptions): T {
    return this.#membrane.wrap(target, document, options);
  }

  /** Convenience for the restricted cross-origin membrane. */
  wrapCrossOrigin<T extends object>(target: T, document: object, expose: ExposeSchema): T {
    return this.#membrane.wrap(target, document, { crossOrigin: true, expose });
  }

  /** Revoke every outstanding capability (new revocation generation). */
  revokeAll(): void {
    this.#membrane.revokeAll();
  }

  /** Invalidate every capability bound to a document after navigation. */
  navigate(document: object): void {
    this.#membrane.navigate(document);
  }

  /** Host-side check used in tests/diagnostics. */
  isWrapper(value: unknown): boolean {
    return this.#membrane.isWrapper(value);
  }

  /** Host-side recovery of the trusted target (never exposed to scripts). */
  unwrap<T>(value: T): T {
    return this.#membrane.unwrap(value);
  }
}

export { SecurityError };
export type { ExposeSchema, WrapOptions };
