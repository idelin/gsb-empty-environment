export { HeaderBag } from "./headers.ts";
export type {
  HeaderPrimitive,
  HeaderReader,
  HeaderSource,
  HeaderValue,
} from "./headers.ts";

export { FedCMValidationError } from "./errors.ts";
export type {
  FedCMResult,
  FedCMResultError,
  FedCMResultOk,
  FedCMValidationErrorCode,
} from "./errors.ts";

export type { FedCMHttpRequest, RedirectOptions } from "./request.ts";
export { parseOrigin, parseUrl, wasRedirected } from "./request.ts";

export type {
  ClientMetadata,
  ClientMetadataSource,
  FedCMAccount,
  FedCMEndpoint,
  FedCMSession,
  SessionLookup,
  SessionSource,
} from "./types.ts";

export { getAccountId, normalizeAccountId } from "./util.ts";

export {
  assertAccountsRequest,
  validateAccountsRequest,
} from "./accounts.ts";
export type {
  AccountsRequestOptions,
  ValidatedAccountsRequest,
} from "./accounts.ts";

export {
  assertClientMetadataRequest,
  validateClientMetadataRequest,
} from "./client-metadata.ts";
export type {
  ClientMetadataRequestOptions,
  ValidatedClientMetadataRequest,
} from "./client-metadata.ts";

export {
  assertIdAssertionRequest,
  createIdAssertionClaimsFromRequest,
  parseIdAssertionBody,
  validateIdAssertionRequest,
} from "./id-assertion.ts";
export type {
  IdAssertionHttpRequest,
  IdAssertionRequestOptions,
  ParsedIdAssertionBody,
  ValidatedIdAssertionRequest,
} from "./id-assertion.ts";

export {
  assertNoUndisclosedEmail,
  assertionContainsEmail,
  buildIdAssertionClaims,
  disclosureAllowsEmail,
  normalizeDisclosureTextShown,
  sanitizeAssertionClaims,
} from "./disclosure.ts";
export type { BuildIdAssertionClaimsOptions } from "./disclosure.ts";

export { assertFedCMRequest, validateFedCMRequest } from "./dispatch.ts";
export type {
  FedCMValidationInput,
  ValidatedFedCMRequest,
} from "./dispatch.ts";

export { assertResponseNotRedirect, isRedirectStatus } from "./redirect.ts";
