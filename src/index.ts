export { ResponseCache, type ResponseCacheOptions } from "./cache.ts";
export {
  OPAQUE_ORIGIN,
  cacheKey,
  originOf,
  sameOrigin,
  type Origin,
} from "./origin.ts";
export {
  integritySatisfied,
  isValidIntegrityMetadata,
  normalizeIntegrity,
} from "./integrity.ts";
export type {
  CacheEntryInput,
  CachedResponse,
  CredentialsMode,
  IntegrityMetadata,
  IntegrityState,
  MatchResult,
  MissDiagnostics,
  RedirectMode,
  RequestMode,
  ResponseType,
  StructuredRequest,
} from "./types.ts";
