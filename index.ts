export { verifyTrustChain } from './src/verifier.ts';
export { TrustChainError } from './src/errors.ts';
export { nodeCryptoVerifier } from './src/crypto-verifier.ts';
export { parseCompactJwt, isEntityConfiguration } from './src/jwt.ts';
export {
  combineMetadataPolicies,
  isSubsetOf,
} from './src/metadata-policy.ts';
export type {
  CompactJwt,
  FederationClaims,
  Jwk,
  Jwks,
  JwsHeader,
  MetadataPolicy,
  PolicyOperators,
  SignatureContext,
  SignatureVerifier,
  TrustChainErrorCode,
  TrustChainResult,
  VerifyTrustChainOptions,
} from './src/types.ts';
