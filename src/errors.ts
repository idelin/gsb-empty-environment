/**
 * Thrown for every failed brand / capability check.
 *
 * A dedicated error type lets host code distinguish security failures
 * (forgery, revocation, document navigation, policy denial) from ordinary
 * runtime errors thrown by the wrapped object itself.
 */
export class SecurityError extends Error {
  override readonly name = 'SecurityError';

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    Object.setPrototypeOf(this, SecurityError.prototype);
  }
}
