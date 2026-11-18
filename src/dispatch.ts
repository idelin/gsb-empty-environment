import {
  validateAccountsRequest,
  type AccountsRequestOptions,
  type ValidatedAccountsRequest,
} from "./accounts.ts";
import {
  validateClientMetadataRequest,
  type ClientMetadataRequestOptions,
  type ValidatedClientMetadataRequest,
} from "./client-metadata.ts";
import {
  validateIdAssertionRequest,
  type IdAssertionHttpRequest,
  type IdAssertionRequestOptions,
  type ValidatedIdAssertionRequest,
} from "./id-assertion.ts";
import {
  validationError,
  type FedCMResult,
} from "./errors.ts";
import type { FedCMHttpRequest } from "./request.ts";

export type ValidatedFedCMRequest =
  | ValidatedAccountsRequest
  | ValidatedClientMetadataRequest
  | ValidatedIdAssertionRequest;

export type FedCMValidationInput =
  | {
      endpoint: "accounts";
      request: FedCMHttpRequest;
      options?: AccountsRequestOptions;
    }
  | {
      endpoint: "client_metadata";
      request: FedCMHttpRequest;
      options?: ClientMetadataRequestOptions;
    }
  | {
      endpoint: "id_assertion";
      request: IdAssertionHttpRequest;
      options: IdAssertionRequestOptions;
    };

export function validateFedCMRequest(
  input: FedCMValidationInput,
): FedCMResult<ValidatedFedCMRequest> {
  switch (input.endpoint) {
    case "accounts":
      return validateAccountsRequest(
        input.request,
        input.options,
      ) as FedCMResult<ValidatedFedCMRequest>;
    case "client_metadata":
      return validateClientMetadataRequest(
        input.request,
        input.options,
      ) as FedCMResult<ValidatedFedCMRequest>;
    case "id_assertion":
      return validateIdAssertionRequest(
        input.request,
        input.options,
      ) as FedCMResult<ValidatedFedCMRequest>;
    default:
      return validationError(
        "invalid_request",
        `Unknown FedCM endpoint: ${String(
          (input as { endpoint?: unknown }).endpoint,
        )}`,
        400,
      );
  }
}

export function assertFedCMRequest(
  input: FedCMValidationInput,
): ValidatedFedCMRequest {
  const result = validateFedCMRequest(input);
  if (!result.ok) throw result.error;
  return result.value;
}
