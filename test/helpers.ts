import {
  generateKeyPairSync,
  sign as cryptoSign,
  createSign,
  type KeyObject,
} from 'node:crypto';
import type {
  CompactJwt,
  FederationClaims,
  Jwk,
  JwsHeader,
  SignatureVerifier,
} from '../src/types.ts';

export interface EntityKey {
  kid: string;
  alg: string;
  publicJwk: Jwk;
  privateKey: KeyObject;
}

export function generateEd25519Key(kid: string): EntityKey {
  const { publicKey, privateKey } = generateKeyPairSync('ed25519');
  const publicJwk = publicKey.export({ format: 'jwk' }) as Jwk;
  publicJwk.kid = kid;
  publicJwk.alg = 'EdDSA';
  publicJwk.use = 'sig';
  return { kid, alg: 'EdDSA', publicJwk, privateKey };
}

export function generateEs256Key(kid: string): EntityKey {
  const { publicKey, privateKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-256',
  });
  const publicJwk = publicKey.export({ format: 'jwk' }) as Jwk;
  publicJwk.kid = kid;
  publicJwk.alg = 'ES256';
  publicJwk.use = 'sig';
  return { kid, alg: 'ES256', publicJwk, privateKey };
}

export function generateEs384Key(kid: string): EntityKey {
  const { publicKey, privateKey } = generateKeyPairSync('ec', {
    namedCurve: 'P-384',
  });
  const publicJwk = publicKey.export({ format: 'jwk' }) as Jwk;
  publicJwk.kid = kid;
  publicJwk.alg = 'ES384';
  publicJwk.use = 'sig';
  return { kid, alg: 'ES384', publicJwk, privateKey };
}

function base64Url(input: Buffer | string): string {
  return Buffer.from(input)
    .toString('base64')
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/g, '');
}

/** Sign a payload into a compact JWT with the given key. */
export function signJwt(
  payload: FederationClaims,
  key: EntityKey,
  headerOverrides: Partial<JwsHeader> = {},
): CompactJwt {
  const header: JwsHeader = {
    alg: key.alg,
    kid: key.kid,
    typ: 'entity-statement+jwt',
    ...headerOverrides,
  };
  const signingInput = `${base64Url(JSON.stringify(header))}.${base64Url(
    JSON.stringify(payload),
  )}`;
  const data = Buffer.from(signingInput, 'ascii');
  let signature: Buffer;
  if (key.alg === 'EdDSA') {
    signature = cryptoSign(null, data, key.privateKey);
  } else if (key.alg.startsWith('ES')) {
    const hash =
      key.alg === 'ES384' ? 'sha384' : key.alg === 'ES512' ? 'sha512' : 'sha256';
    signature = cryptoSign(hash, data, {
      key: key.privateKey,
      dsaEncoding: 'ieee-p1363',
    });
  } else {
    signature = createSign('sha256').update(data).sign(key.privateKey);
  }
  return `${signingInput}.${base64Url(signature)}`;
}

/** Mangle the signature segment so verification is guaranteed to fail. */
export function tamperSignature(jwt: CompactJwt): CompactJwt {
  const parts = jwt.split('.');
  const flipped = parts[2].startsWith('A')
    ? `B${parts[2].slice(1)}`
    : `A${parts[2].slice(1)}`;
  return `${parts[0]}.${parts[1]}.${flipped}`;
}

export interface ChainKeyring {
  /** Map of entity identifier -> key used by that entity. */
  byEntity: Map<string, EntityKey>;
}

/**
 * Verifier that mirrors real federation key resolution but keeps keys in
 * memory: it looks up the key registered for the statement's issuer and
 * checks the signature segment. This still exercises the full
 * verifyTrustChain flow including the injected verifier contract.
 */
export function inMemoryVerifier(keyring: ChainKeyring): SignatureVerifier {
  return (context) => {
    const key = keyring.byEntity.get(context.payload.iss);
    if (key === undefined) {
      throw new Error(`no registered key for issuer ${context.payload.iss}`);
    }
    const expected = signJwt(context.payload, key, {
      alg: context.header.alg,
      kid: context.header.kid,
      typ: context.header.typ,
    });
    if (expected.split('.')[2] !== context.jwt.split('.')[2]) {
      throw new Error('signature mismatch');
    }
  };
}

export const HOUR = 60 * 60;
export const DAY = 24 * HOUR;

export interface BuildInput {
  leafId: string;
  intermediateIds: string[];
  anchorId: string;
  leafKey: EntityKey;
  intermediateKeys: EntityKey[];
  anchorKey: EntityKey;
  now: number;
  ttlSeconds?: number;
  /** max_path_length placed on each Subordinate Statement (leaf-side first). */
  maxPathLengthByIndex?: number[];
  /** metadata_policy placed on each Subordinate Statement (leaf-side first). */
  policyByIndex?: FederationClaims['metadata_policy'][];
}

/**
 * Build a cryptographically well-formed trust chain leaf-first:
 * leaf EC, one Subordinate Statement per hierarchy edge, anchor EC.
 *
 * The entity hierarchy is:
 *   leaf -> intermediate[0] -> ... -> intermediate[n-1] -> anchor
 *
 * Per OpenID Federation 1.0 the array contains no self-signed Entity
 * Configuration for intermediates. Instead each Subordinate Statement's
 * `jwks` publishes the signing keys of its subject (the subordinate). The
 * statement at chain index 1 is issued by intermediate[0] about the leaf,
 * and so on; the final Subordinate Statement is issued by the anchor.
 */
export function buildChain(input: BuildInput): CompactJwt[] {
  const ttl = input.ttlSeconds ?? HOUR;
  const chain: CompactJwt[] = [];
  // Hierarchy levels: 0 = leaf, intermediates, last = anchor.
  const entityIds = [
    input.leafId,
    ...input.intermediateIds,
    input.anchorId,
  ];
  const entityKeys = [
    input.leafKey,
    ...input.intermediateKeys,
    input.anchorKey,
  ];

  const leafEc: FederationClaims = {
    iss: input.leafId,
    sub: input.leafId,
    iat: input.now,
    exp: input.now + ttl,
    jwks: { keys: [input.leafKey.publicJwk] },
  };
  chain.push(signJwt(leafEc, input.leafKey));

  // One Subordinate Statement per hierarchy edge, leaf-side first. Statement
  // at chain index i (i >= 1) is issued by level i about level i-1, and its
  // jwks carries the subject's keys so the next statement can be verified.
  for (let level = 1; level < entityIds.length; level += 1) {
    const optionIndex = level - 1;
    const subordinate: FederationClaims = {
      iss: entityIds[level],
      sub: entityIds[level - 1],
      iat: input.now,
      exp: input.now + ttl,
      ...(input.maxPathLengthByIndex?.[optionIndex] !== undefined
        ? { max_path_length: input.maxPathLengthByIndex[optionIndex] }
        : {}),
      ...(input.policyByIndex?.[optionIndex] !== undefined
        ? { metadata_policy: input.policyByIndex[optionIndex] }
        : {}),
      jwks: { keys: [entityKeys[level - 1].publicJwk] },
    };
    chain.push(signJwt(subordinate, entityKeys[level]));
  }

  const anchorEc: FederationClaims = {
    iss: input.anchorId,
    sub: input.anchorId,
    iat: input.now,
    exp: input.now + ttl,
    jwks: { keys: [input.anchorKey.publicJwk] },
  };
  chain.push(signJwt(anchorEc, input.anchorKey));

  return chain;
}

export function buildKeyring(
  entries: Array<[string, EntityKey]>,
): ChainKeyring {
  return { byEntity: new Map(entries) };
}
