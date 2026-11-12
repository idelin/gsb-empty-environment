export {
  FedcmError,
  isFedcmError,
  type FedcmErrorInit,
  type FedcmErrorReason,
} from "./src/errors.ts";

export {
  SEC_FETCH_DEST_WEBIDENTITY,
  assertJsonContentType,
  assertMethod,
  assertNoCookie,
  assertNoOrigin,
  assertNoReferer,
  assertWebIdentity,
  getHeader,
  jsonResponse,
  parseCookieHeader,
  readCookies,
  type FedcmRequest,
} from "./src/headers.ts";

export {
  accountsResponse,
  assertNotRedirect,
  noAccountsResponse,
  validateAccountsRequest,
  type FedcmAccount,
  type FedcmAccountsBody,
  type ValidatedAccountsRequest,
} from "./src/accounts.ts";

export {
  clientMetadataResponse,
  validateClientMetadataRequest,
  type ClientMetadata,
  type ClientRegistryLookup,
  type RegisteredClient,
  type ValidatedClientMetadataRequest,
} from "./src/client-metadata.ts";

export {
  validateIdAssertionRequest,
  type IdAssertionDeps,
  type IdAssertionRequestBody,
  type SessionContext,
  type ValidatedIdAssertion,
} from "./src/id-assertion.ts";

export {
  assertEmailDisclosure,
  withEmailClaim,
  type AssertionClaims,
} from "./src/disclosure.ts";

export {
  createFedcmHandler,
  toFedcmErrorResponse,
} from "./src/handler.ts";

export {
  nodeHttpHandler,
  sendWebResponse,
  toWebRequest,
} from "./src/http-adapter.ts";
