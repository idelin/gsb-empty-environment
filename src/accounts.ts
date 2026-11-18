import {
  validationError,
  validationOk,
  type FedCMResult,
} from "./errors.ts";
import {
  parseUrl,
  requestHeaders,
  wasRedirected,
  type FedCMHttpRequest,
  type RedirectOptions,
} from "./request.ts";
import type { HeaderBag } from "./headers.ts";

export interface AccountsRequestOptions extends RedirectOptions {
  /** The FedCM accounts endpoint is a GET endpoint. Defaults to `true`. */
  requireGet?: boolean;
}

export interface ValidatedAccountsRequest {
  endpoint: "accounts";
  method: string;
  url: URL | null;
  clientId: string | null;
  headers: HeaderBag;
  redirected: false;
}

/**
 * Validates a request to the FedCM accounts endpoint.
 *
 * A browser-driven FedCM request has `Sec-Fetch-Dest: webidentity` and no
 * `Origin` or `Referer`. A page calling `fetch()` directly has a different
 * destination and normally carries `Origin`/`Referer`, so both checks matter.
 */
export function validateAccountsRequest(
  request: FedCMHttpRequest,
  options: AccountsRequestOptions = {},
): FedCMResult<ValidatedAccountsRequest> {
  const headers = requestHeaders(request);
  const method = (request.method ?? "GET").toUpperCase();

  if (options.requireGet !== false && method !== "GET") {
    return validationError(
      "method_not_allowed",
      `accounts endpoint only accepts GET, got ${method || "(missing)"}`,
      405,
    );
  }

  const destinationValues = headers.getAll("sec-fetch-dest");
  if (destinationValues.length === 0) {
    return validationError(
      "missing_sec_fetch_dest",
      "accounts requests must include Sec-Fetch-Dest: webidentity",
    );
  }

  const normalizedDestinations = destinationValues
    .map((value) => value.trim().toLowerCase())
    .filter((value) => value.length > 0);

  if (
    normalizedDestinations.length !== 1 ||
    normalizedDestinations[0] !== "webidentity"
  ) {
    return validationError(
      "invalid_sec_fetch_dest",
      "accounts requests must have exactly Sec-Fetch-Dest: webidentity",
    );
  }

  if (headers.has("origin")) {
    return validationError(
      "unexpected_origin",
      "accounts requests must not include an Origin header",
    );
  }

  if (headers.has("referer")) {
    return validationError(
      "unexpected_referer",
      "accounts requests must not include a Referer header",
    );
  }

  if (wasRedirected(request, options)) {
    return validationError(
      "redirect_not_allowed",
      "accounts requests must not follow redirects",
    );
  }

  const url = parseUrl(request.url);
  return validationOk({
    endpoint: "accounts",
    method,
    url,
    clientId: url?.searchParams.get("client_id") ?? null,
    headers,
    redirected: false,
  });
}

export function assertAccountsRequest(
  request: FedCMHttpRequest,
  options: AccountsRequestOptions = {},
): ValidatedAccountsRequest {
  const result = validateAccountsRequest(request, options);
  if (!result.ok) throw result.error;
  return result.value;
}
