/**
 * Error hierarchy for the description fetcher.
 *
 * Every security rejection uses a distinct error class (and a stable
 * `code`) so that the device-discovery caller can distinguish a hostile
 * SSDP response from an ordinary network failure without string matching.
 */

export type FetchErrorCode =
  | "INVALID_LOCATION"
  | "HOST_MISMATCH"
  | "NON_PRIVATE_HOST"
  | "UNSAFE_SCHEME"
  | "UNSAFE_PORT"
  | "REDIRECT_HOST_MISMATCH"
  | "TOO_MANY_REDIRECTS"
  | "BAD_RESPONSE"
  | "XML_TOO_LARGE"
  | "UNSAFE_XML"
  | "MALFORMED_XML"
  | "URLBASE_HOST_MISMATCH"
  | "CONTROLURL_HOST_MISMATCH"
  | "MALFORMED_SERVICE"
  | "SERVICE_NOT_DECLARED"
  | "HTTP_ERROR";

export class DescriptionFetchError extends Error {
  readonly code: FetchErrorCode;

  constructor(code: FetchErrorCode, message: string) {
    super(message);
    this.name = "DescriptionFetchError";
    this.code = code;
  }
}

export class InvalidLocationError extends DescriptionFetchError {
  constructor(message: string) {
    super("INVALID_LOCATION", message);
    this.name = "InvalidLocationError";
  }
}

export class HostMismatchError extends DescriptionFetchError {
  constructor(message: string) {
    super("HOST_MISMATCH", message);
    this.name = "HostMismatchError";
  }
}

export class NonPrivateHostError extends DescriptionFetchError {
  constructor(message: string) {
    super("NON_PRIVATE_HOST", message);
    this.name = "NonPrivateHostError";
  }
}

export class UnsafeSchemeError extends DescriptionFetchError {
  constructor(message: string) {
    super("UNSAFE_SCHEME", message);
    this.name = "UnsafeSchemeError";
  }
}

export class UnsafePortError extends DescriptionFetchError {
  constructor(message: string) {
    super("UNSAFE_PORT", message);
    this.name = "UnsafePortError";
  }
}

export class RedirectHostMismatchError extends DescriptionFetchError {
  constructor(message: string) {
    super("REDIRECT_HOST_MISMATCH", message);
    this.name = "RedirectHostMismatchError";
  }
}

export class TooManyRedirectsError extends DescriptionFetchError {
  constructor(message: string) {
    super("TOO_MANY_REDIRECTS", message);
    this.name = "TooManyRedirectsError";
  }
}

export class BadResponseError extends DescriptionFetchError {
  constructor(message: string) {
    super("BAD_RESPONSE", message);
    this.name = "BadResponseError";
  }
}

export class XmlTooLargeError extends DescriptionFetchError {
  constructor(message: string) {
    super("XML_TOO_LARGE", message);
    this.name = "XmlTooLargeError";
  }
}

export class UnsafeXmlError extends DescriptionFetchError {
  constructor(message: string) {
    super("UNSAFE_XML", message);
    this.name = "UnsafeXmlError";
  }
}

export class MalformedXmlError extends DescriptionFetchError {
  constructor(message: string) {
    super("MALFORMED_XML", message);
    this.name = "MalformedXmlError";
  }
}

export class UrlBaseHostMismatchError extends DescriptionFetchError {
  constructor(message: string) {
    super("URLBASE_HOST_MISMATCH", message);
    this.name = "UrlBaseHostMismatchError";
  }
}

export class ControlUrlHostMismatchError extends DescriptionFetchError {
  constructor(message: string) {
    super("CONTROLURL_HOST_MISMATCH", message);
    this.name = "ControlUrlHostMismatchError";
  }
}

export class MalformedServiceError extends DescriptionFetchError {
  constructor(message: string) {
    super("MALFORMED_SERVICE", message);
    this.name = "MalformedServiceError";
  }
}

export class ServiceNotDeclaredError extends DescriptionFetchError {
  constructor(message: string) {
    super("SERVICE_NOT_DECLARED", message);
    this.name = "ServiceNotDeclaredError";
  }
}

export class HttpStatusError extends DescriptionFetchError {
  readonly status: number;

  constructor(status: number, message: string) {
    super("HTTP_ERROR", message);
    this.name = "HttpStatusError";
    this.status = status;
  }
}
