import type { MigrationResult } from './migrate.ts';
import type { CompiledField, CompiledPlan } from './plan.ts';

/**
 * Builds the inverse of a compiled plan: every rule's direction is swapped
 * and its affine is inverted exactly (ratio -> 1/r, offset -> -o/r).
 *
 * The inverse never claims to be lossless where the forward migration lost
 * information: every concrete path converted in the forward pass is tagged
 * via `losslessByPath`, and audit entries produced from an inverse plan
 * carry `lossless: false` plus an explanatory note wherever the forward
 * pass rounded. Precision is reset to `exact` — rounding already happened
 * in the forward pass and cannot be undone.
 *
 * Constraints are not carried over: they were validated against the forward
 * target shape and do not apply to the inverse output.
 */
export function invertPlan(
  plan: CompiledPlan,
  forward: Extract<MigrationResult, { ok: true }>,
): CompiledPlan {
  const losslessByPath = new Map<string, boolean>(
    forward.audit.map((entry) => [entry.path, !entry.lossy]),
  );
  const fields: CompiledField[] = plan.fields.map((f) => {
    return {
      ...f,
      from: f.to,
      to: f.from,
      affine: {
        ratio: f.affine.ratio.inv(),
        offset: f.affine.offset.neg().div(f.affine.ratio),
      },
      precision: { type: 'exact' },
    };
  });
  return { registry: plan.registry, fields, constraints: [], losslessByPath };
}
