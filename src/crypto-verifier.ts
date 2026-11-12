import { createPublicKey, createVerify, verify as cryptoVerify } from 'node:crypto';
import { TrustChainError } from './errors.ts';
import { parseCompactJwt } from './jwt.ts';
import type { Jwk, SignatureContext, SignatureVerifier } from './types.ts';

type RsaAlg = 'RS256' | 'RS384' | 'RS512' | 'PS256' | 'PS384' | 'PS512';
type PsAlg = 'PS256' | 'PS384' | 'PS512';
type EcAlg = 'ES256' | 'ES384' | 'ES512';
type SupportedAlg = RsaAlg | EcAlg | 'EdDSA';

const RSA_HASH: Record<RsaAlg, string> = {
  RS256: 'sha256',
  RS384: 'sha384',
  RS512: 'sha512',
  PS256: 'sha256',
  PS384: 'sha384',
  PS512: 'sha512',
};

const EC_HASH: Record<EcAlg, string> = {
  ES256: 'sha256',
  ES384: 'sha384',
  ES512: 'sha512',
};

const PSS_SALT_LENGTH: Record<PsAlg, number> = {
  PS256: 32,
  PS384: 48,
  PS512: 64,
};

function algMatchesKey(alg: SupportedAlg, key: Jwk): boolean {
  if (key.alg !== undefined && key.alg !== alg) {
    return false;
  }
  if (alg.startsWith('RS') || alg.startsWith('PS')) {
    return key.kty === 'RSA';
  }
  if (alg.startsWith('ES')) {
    return key.kty === 'EC';
  }
  return key.kty === 'OKP' && (key.crv === 'Ed25519' || key.crv === 'Ed448');
}

function selectKey(context: SignatureContext): Jwk {
  const alg = context.header.alg as SupportedAlg;
  if (alg === 'none') {
    throw new TrustChainError('INVALID_SIGNATURE', 'alg "none" is not accepted');
  }
  const candidates = context.jwks.keys.filter((key) => {
    if (context.header.kid !== undefined && key.kid !== context.header.kid) {
      return false;
    }
    return algMatchesKey(alg, key);
  });
  if (candidates.length === 0) {
    throw new TrustChainError(
      'INVALID_SIGNATURE',
      `no matching JWK in jwks for alg=${context.header.alg}` +
        (context.header.kid !== undefined ? ` kid=${context.header.kid}` : ''),
    );
  }
  return candidates[0];
}

/**
 * Signature verifier backed by Node's built-in `crypto`. Supports the
 * federation signature algorithms registered for RFC 7518 / RFC 8037:
 * RS256/384/512, PS256/384/512, ES256/384/512 and EdDSA (Ed25519/Ed448).
 *
 * Pass this as `options.verifier`, or supply your own implementation that
 * talks to an HSM/KMS.
 */
export const nodeCryptoVerifier: SignatureVerifier = (context): void => {
  const alg = context.header.alg as SupportedAlg;
  const jwk = selectKey(context);
  const publicKey = createPublicKey({ key: jwk, format: 'jwk' });
  const parsed = parseCompactJwt(context.jwt);
  const signingInput = Buffer.from(parsed.signingInput, 'ascii');
  const signature = Buffer.from(parsed.signature);

  let valid: boolean;
  if (alg === 'EdDSA') {
    valid = cryptoVerify(null, signingInput, publicKey, signature);
  } else if (alg.startsWith('ES')) {
    valid = cryptoVerify(
      EC_HASH[alg as EcAlg],
      signingInput,
      { key: publicKey, dsaEncoding: 'ieee-p1363' },
      signature,
    );
  } else if (alg.startsWith('PS')) {
    valid = createVerify(RSA_HASH[alg as RsaAlg])
      .update(signingInput)
      .verify(
        {
          key: publicKey,
          padding: 9, // RSA_PKCS1_PSS_PADDING
          saltLength: PSS_SALT_LENGTH[alg as PsAlg],
        },
        signature,
      );
  } else {
    valid = createVerify(RSA_HASH[alg as RsaAlg])
      .update(signingInput)
      .verify(publicKey, signature);
  }

  if (!valid) {
    throw new TrustChainError(
      'INVALID_SIGNATURE',
      `JWS signature does not verify under alg=${alg}`,
    );
  }
};
