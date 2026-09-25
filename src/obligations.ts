export interface QuotaObligation {
  readonly kind: 'quota';
  readonly resource: string;
  readonly limit: number;
}

export interface FieldMaskObligation {
  readonly kind: 'field-mask';
  readonly fields: readonly string[];
}

export interface OutputFormatObligation {
  readonly kind: 'output-format';
  readonly format: string;
}

export type Obligation = QuotaObligation | FieldMaskObligation | OutputFormatObligation;

export interface ObligationConflict {
  readonly kind: 'output-format';
  readonly formats: readonly string[];
}

export type MergeResult =
  | { readonly ok: true; readonly obligations: readonly Obligation[] }
  | { readonly ok: false; readonly conflict: ObligationConflict };

/**
 * Obligation merge algebra (a semilattice with a conflict bottom):
 *  - quota:        same resource joins to the tighter (minimum) limit;
 *  - field-mask:   field sets join by union;
 *  - output-format: identical formats join; distinct formats have no join
 *                  and produce a conflict instead of a silent pick.
 * The result is canonically ordered, so merge is commutative, associative
 * and idempotent: traversal order never changes the outcome.
 */
export function mergeObligations(
  left: readonly Obligation[],
  right: readonly Obligation[],
): MergeResult {
  const quotas = new Map<string, number>();
  const maskedFields = new Set<string>();
  const formats = new Set<string>();

  for (const obligation of [...left, ...right]) {
    switch (obligation.kind) {
      case 'quota': {
        const existing = quotas.get(obligation.resource);
        quotas.set(
          obligation.resource,
          existing === undefined ? obligation.limit : Math.min(existing, obligation.limit),
        );
        break;
      }
      case 'field-mask':
        for (const field of obligation.fields) maskedFields.add(field);
        break;
      case 'output-format':
        formats.add(obligation.format);
        break;
    }
  }

  if (formats.size > 1) {
    return { ok: false, conflict: { kind: 'output-format', formats: [...formats].sort() } };
  }

  const obligations: Obligation[] = [];
  for (const resource of [...quotas.keys()].sort()) {
    obligations.push({ kind: 'quota', resource, limit: quotas.get(resource)! });
  }
  if (maskedFields.size > 0) {
    obligations.push({ kind: 'field-mask', fields: [...maskedFields].sort() });
  }
  for (const format of [...formats].sort()) {
    obligations.push({ kind: 'output-format', format });
  }
  return { ok: true, obligations };
}

export function mergeAll(parts: readonly (readonly Obligation[])[]): MergeResult {
  let acc: readonly Obligation[] = [];
  for (const part of parts) {
    const merged = mergeObligations(acc, part);
    if (!merged.ok) return merged;
    acc = merged.obligations;
  }
  return { ok: true, obligations: acc };
}
