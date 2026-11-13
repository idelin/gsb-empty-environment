import { OPAQUE_ORIGIN, parseOrigin } from './origin.ts';

/**
 * Declarative fence policy supplied by the embedding shell.
 *
 * The fence document's own origin is always allowed; `allowedOrigins` only
 * lists additional cross-origin network destinations.
 */
export interface FencePolicy {
  /** Serialized origin of the document hosted inside the fence. */
  fenceOrigin: string;
  /** Absolute URL used to resolve relative requests (defaults to fenceOrigin). */
  documentBaseUrl?: string;
  /** Extra origins the fenced document may contact over the network. */
  allowedOrigins?: readonly string[];
}

/** Policy after validation/normalization, consumed by the adjudicator. */
export interface CompiledPolicy {
  readonly [COMPILED_POLICY_BRAND]: true;
  fenceOrigin: string;
  documentBaseUrl: string;
  allowedOrigins: ReadonlySet<string>;
}

/** Runtime brand distinguishing compiled policies from raw user input. */
export const COMPILED_POLICY_BRAND: unique symbol = Symbol.for('fence.compiled-policy');

/**
 * Validate and normalize a {@link FencePolicy}.
 *
 * Opaque (`"null"`) and unparseable entries are rejected because they cannot
 * name a concrete network destination. Duplicates collapse into the set.
 */
export function compilePolicy(policy: FencePolicy): CompiledPolicy {
  const fenceOrigin = parseOrigin(policy.fenceOrigin);
  if (fenceOrigin === OPAQUE_ORIGIN) {
    throw new TypeError(`Fence origin must be a concrete origin, got ${policy.fenceOrigin}`);
  }

  const documentBaseUrl = policy.documentBaseUrl ?? `${fenceOrigin}/`;
  const baseOrigin = parseOrigin(documentBaseUrl);
  if (baseOrigin !== fenceOrigin) {
    throw new TypeError(
      `documentBaseUrl origin ${baseOrigin} must match fence origin ${fenceOrigin}`,
    );
  }

  const allowedOrigins = new Set<string>();
  for (const rawOrigin of policy.allowedOrigins ?? []) {
    const origin = parseOrigin(rawOrigin);
    if (origin === OPAQUE_ORIGIN) {
      throw new TypeError(`Opaque origins cannot be allow-listed: ${rawOrigin}`);
    }
    allowedOrigins.add(origin);
  }

  return {
    [COMPILED_POLICY_BRAND]: true,
    fenceOrigin,
    documentBaseUrl,
    allowedOrigins,
  };
}
