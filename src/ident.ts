export const IDENTIFIER_RE = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/;

export function assertIdentifier(value: string, role: string): void {
  if (typeof value !== 'string' || !IDENTIFIER_RE.test(value)) {
    throw new TypeError(`invalid ${role} identifier: ${String(value)}`);
  }
}
