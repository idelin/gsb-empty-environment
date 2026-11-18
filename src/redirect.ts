import { FedCMValidationError } from "./errors.ts";

export function isRedirectStatus(status: number): boolean {
  return Number.isInteger(status) && status >= 300 && status < 400;
}

/**
 * Guard for endpoint handlers: a FedCM endpoint response must not be a
 * redirect. Use this before returning the handler's status code.
 */
export function assertResponseNotRedirect(status: number): void {
  if (isRedirectStatus(status)) {
    throw new FedCMValidationError(
      "redirect_not_allowed",
      "FedCM endpoint responses must not redirect",
      403,
    );
  }
}
