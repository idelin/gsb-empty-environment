import { TrustChainError, signatureError } from './errors.ts';
import {
  isEntityConfiguration,
  parseCompactJwt,
  requireJwks,
} from './jwt.ts';
import { combineMetadataPolicies } from './metadata-policy.ts';
import type {
  CompactJwt,
  FederationClaims,
  TrustChainResult,
  VerifyTrustChainOptions,
} from './types.ts';

interface ParsedStatement {
  jwt: CompactJwt;
  header: ReturnType<typeof parseCompactJwt>['header'];
  payload: FederationClaims;
}

function assertTimeWindow(
  payload: FederationClaims,
  nowSeconds: number,
  skew: number,
): void {
  if (payload.iat !== undefined && typeof payload.iat === 'number') {
    if (payload.iat > nowSeconds + skew) {
      throw new TrustChainError(
        'NOT_YET_VALID',
        `statement issued by ${payload.iss} for ${payload.sub} is not valid yet ` +
          `(iat=${payload.iat}, now=${nowSeconds})`,
      );
    }
  }
  if (payload.exp < nowSeconds - skew) {
    throw new TrustChainError(
      'EXPIRED',
      `statement issued by ${payload.iss} for ${payload.sub} expired ` +
        `(exp=${payload.exp}, now=${nowSeconds})`,
    );
  }
}

/**
 * Validate an OpenID Federation trust chain.
 *
 * `chain` is ordered leaf-first: the leaf Entity Configuration, zero or
 * more Subordinate Statements walking up the federation, and finally the
 * trust anchor Entity Configuration.
 */
export async function verifyTrustChain(
  chain: CompactJwt[],
  options: VerifyTrustChainOptions,
): Promise<TrustChainResult> {
  if (!options || typeof options.verifier !== 'function') {
    throw new TypeError('options.verifier must be an injected SignatureVerifier function');
  }
  if (!Array.isArray(chain) || chain.length < 2) {
    throw new TrustChainError(
      'EMPTY_CHAIN',
      'trust chain must contain at least a leaf Entity Configuration and a trust anchor',
    );
  }

  const nowSeconds = Math.floor(
    (options.now ?? Date.now()) / 1000,
  );
  const skew = options.clockSkewSeconds ?? 0;

  const statements: ParsedStatement[] = chain.map((jwt) => {
    const parsed = parseCompactJwt(jwt);
    return { jwt, header: parsed.header, payload: parsed.payload };
  });

  const lastIndex = statements.length - 1;
  const leaf = statements[0];
  const anchor = statements[lastIndex];

  // ---- Structural role checks ---------------------------------------------
  if (!isEntityConfiguration(leaf.payload)) {
    throw new TrustChainError(
      'NOT_ENTITY_CONFIGURATION',
      'first statement must be a self-signed leaf Entity Configuration (iss === sub)',
    );
  }
  if (!isEntityConfiguration(anchor.payload)) {
    throw new TrustChainError(
      'NOT_ENTITY_CONFIGURATION',
      'last statement must be a self-signed trust anchor Entity Configuration (iss === sub)',
    );
  }
  for (let index = 1; index < lastIndex; index += 1) {
    if (isEntityConfiguration(statements[index].payload)) {
      throw new TrustChainError(
        'NOT_SUBORDINATE_STATEMENT',
        `statement at index ${index} is self-signed; only the leaf and the ` +
          'trust anchor may be Entity Configurations',
      );
    }
  }

  // ---- Identifier linkage (defeat forged subordinate claims) --------------
  // Per OIDC Federation 1.0 section 4.2, adjacent statements must interlock:
  // the subject of each statement is the issuer of the next one. The first
  // statement is self-signed (iss === sub), so this guarantees:
  //   chain[0].iss === chain[0].sub === chain[1].sub
  //   chain[1].iss === chain[2].sub, ... and the anchor EC is self-signed.
  for (let index = 0; index < lastIndex; index += 1) {
    const current = statements[index].payload;
    const next = statements[index + 1].payload;
    if (current.iss !== next.sub) {
      throw new TrustChainError(
        'BROKEN_CHAIN',
        `statement at index ${index} is issued by ${current.iss} but the next ` +
          `statement's subject is ${next.sub}; each statement's iss must equal ` +
          "the following statement's sub",
      );
    }
  }

  if (
    options.trustAnchor !== undefined &&
    anchor.payload.sub !== options.trustAnchor
  ) {
    throw new TrustChainError(
      'TRUST_ANCHOR_MISMATCH',
      `chain ends at ${anchor.payload.sub}, expected trust anchor ${options.trustAnchor}`,
    );
  }
  if (
    options.leafEntityId !== undefined &&
    leaf.payload.sub !== options.leafEntityId
  ) {
    throw new TrustChainError(
      'LEAF_MISMATCH',
      `chain starts at ${leaf.payload.sub}, expected leaf ${options.leafEntityId}`,
    );
  }

  // ---- Time windows and effective expiration ------------------------------
  let effectiveExp = Number.POSITIVE_INFINITY;
  for (const { payload } of statements) {
    assertTimeWindow(payload, nowSeconds, skew);
    effectiveExp = Math.min(effectiveExp, payload.exp);
  }

  // ---- Signature verification with injected crypto ------------------------
  // Per the OIDC Federation trust chain validation rules, the signature of
  // statement ES[j] is verified with a public key carried in ES[j+1].jwks,
  // i.e. the jwks of the statement immediately above it. The leaf Entity
  // Configuration (j=0) is self-signed and verifies with its own jwks; the
  // trust anchor Entity Configuration at the end is also self-signed.
  for (let index = 0; index <= lastIndex; index += 1) {
    const statement = statements[index];
    const selfSigned = isEntityConfiguration(statement.payload);
    const jwks = selfSigned
      ? requireJwks(statement.payload)
      : requireJwks(statements[index + 1].payload);
    try {
      await options.verifier({
        jwt: statement.jwt,
        index,
        header: statement.header,
        payload: statement.payload,
        jwks,
      });
    } catch (error) {
      throw signatureError(error);
    }
  }

  // ---- Path length constraints --------------------------------------------
  const subordinateCount = statements.length - 2;
  for (let index = 1; index < lastIndex; index += 1) {
    const payload = statements[index].payload;
    const maxPathLength = payload.max_path_length;
    if (maxPathLength === undefined) {
      continue;
    }
    if (typeof maxPathLength !== 'number' || maxPathLength < 0) {
      throw new TrustChainError(
        'PATH_TOO_LONG',
        `statement at index ${index} has an invalid max_path_length`,
      );
    }
    // Subordinate Statements strictly below this statement's issuer.
    const remaining = index - 1;
    if (remaining > maxPathLength) {
      throw new TrustChainError(
        'PATH_TOO_LONG',
        `statement at index ${index} allows max_path_length=${maxPathLength} ` +
          `but ${remaining} subordinate statement(s) remain below it`,
      );
    }
  }

  // ---- Metadata policy must only narrow -----------------------------------
  const subordinatePolicies: NonNullable<
    FederationClaims['metadata_policy']
  >[] = statements
    .slice(1, lastIndex)
    .map(({ payload }) => payload.metadata_policy)
    .filter((policy): policy is NonNullable<typeof policy> => policy !== undefined);
  const combinedMetadataPolicy = combineMetadataPolicies(subordinatePolicies);

  return {
    leaf: leaf.payload.sub,
    trustAnchor: anchor.payload.sub,
    exp: effectiveExp,
    statements: statements.map(({ payload }) => payload),
    combinedMetadataPolicy,
    pathLength: subordinateCount,
  };
}
