import {
  validationError,
  validationOk,
  type FedCMResult,
} from "./errors.ts";
import { requestHeaders, type FedCMHttpRequest } from "./request.ts";
import type { HeaderBag } from "./headers.ts";

export interface ClientMetadataRequestOptions {
  /** The FedCM client metadata endpoint is fetched with GET. */
  requireGet?: boolean;
}

export interface ValidatedClientMetadataRequest {
  endpoint: "client_metadata";
  method: string;
  headers: HeaderBag;
}

/**
 * Validates a request to the FedCM client metadata endpoint.
 *
 * The user agent fetches client metadata without credentials. A Cookie on
 * this request means it did not come from the FedCM metadata fetch (or that a
 * proxy added credentials) and must be rejected.
 */
export function validateClientMetadataRequest(
  request: FedCMHttpRequest,
  options: ClientMetadataRequestOptions = {},
): FedCMResult<ValidatedClientMetadataRequest> {
  const headers = requestHeaders(request);

  if (headers.has("cookie")) {
    return validationError(
      "unexpected_cookie",
      "client_metadata requests must not include a Cookie header",
      400,
    );
  }

  const method = (request.method ?? "GET").toUpperCase();
  if (
    options.requireGet === true &&
    method !== "GET" &&
    method !== "HEAD"
  ) {
    return validationError(
      "method_not_allowed",
      `client_metadata endpoint only accepts GET, got ${method || "(missing)"}`,
      405,
    );
  }

  return validationOk({
    endpoint: "client_metadata",
    method,
    headers,
  });
}

export function assertClientMetadataRequest(
  request: FedCMHttpRequest,
  options: ClientMetadataRequestOptions = {},
): ValidatedClientMetadataRequest {
  const result = validateClientMetadataRequest(request, options);
  if (!result.ok) throw result.error;
  return result.value;
}
