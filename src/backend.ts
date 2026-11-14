import { BoundaryError } from "./errors.ts";
import type { ForwardedRequest, HeaderEntry } from "./types.ts";

/**
 * Verify that the body handed to the backend is exactly the body described by
 * the boundary. This deliberately uses the boundary object, not a second scan
 * of the request head.
 */
export function verifyForwardedRequest(request: ForwardedRequest): void {
  const { boundary, headers, body } = request;
  const contentLengths = headerValues(headers, "content-length");
  const transferEncodings = headerValues(headers, "transfer-encoding");

  if (boundary.contentLength !== body.length) {
    throw new BoundaryError(
      "BACKEND_BOUNDARY_MISMATCH",
      `decoded body length ${body.length} != boundary contentLength ${boundary.contentLength}`,
    );
  }
  if (boundary.bodyEnd - boundary.bodyStart !== boundary.wireBodyLength) {
    throw new BoundaryError(
      "BACKEND_BOUNDARY_MISMATCH",
      "boundary offsets do not match wire body length",
    );
  }

  if (body.length !== boundary.forwarding.contentLength) {
    throw new BoundaryError(
      "BACKEND_BOUNDARY_MISMATCH",
      `body length ${body.length} != forwarded boundary ${boundary.forwarding.contentLength}`,
    );
  }

  if (boundary.forwarding.mode === "content-length") {
    if (
      boundary.kind !== "content-length" &&
      boundary.kind !== "chunked"
    ) {
      throw new BoundaryError(
        "BACKEND_BOUNDARY_MISMATCH",
        "Content-Length forwarding requires a body framing decision",
      );
    }
    if (contentLengths.length !== 1) {
      throw new BoundaryError(
        "BACKEND_BOUNDARY_MISMATCH",
        "normalized request must have exactly one Content-Length",
      );
    }
    if (contentLengths[0] !== String(boundary.forwarding.contentLength)) {
      throw new BoundaryError(
        "BACKEND_BOUNDARY_MISMATCH",
        "normalized Content-Length does not match boundary",
      );
    }
    if (transferEncodings.length !== 0) {
      throw new BoundaryError(
        "BACKEND_BOUNDARY_MISMATCH",
        "normalized Content-Length request must not carry Transfer-Encoding",
      );
    }
  } else if (
    (boundary.forwarding.mode === "none" && boundary.kind !== "none") ||
    (boundary.forwarding.mode === "upgrade" && boundary.kind !== "upgrade")
  ) {
    throw new BoundaryError(
      "BACKEND_BOUNDARY_MISMATCH",
      "forwarding mode does not match the framing decision",
    );
  } else if (contentLengths.length !== 0 || transferEncodings.length !== 0) {
    throw new BoundaryError(
      "BACKEND_BOUNDARY_MISMATCH",
      "bodyless normalized request must not carry framing headers",
    );
  }
}

export class RecordingBackend {
  readonly requests: ForwardedRequest[] = [];
  readonly verify: boolean;

  constructor(verify = true) {
    this.verify = verify;
  }

  readonly handle = async (request: ForwardedRequest): Promise<void> => {
    if (this.verify) verifyForwardedRequest(request);
    this.requests.push(request);
  };
}

/**
 * Serialize the normalized in-process request into backend wire bytes. The
 * resulting head contains at most one Content-Length and never a
 * Transfer-Encoding, so a backend cannot reach a different boundary.
 */
export function serializeForwardedRequest(request: ForwardedRequest): Buffer {
  const lines = [
    `${request.method} ${request.target} ${request.version}`,
    ...request.headers.map(([name, value]) => `${name}: ${value}`),
    "",
    "",
  ];
  return Buffer.concat([Buffer.from(lines.join("\r\n"), "latin1"), request.body]);
}

function headerValues(
  headers: ReadonlyArray<HeaderEntry>,
  name: string,
): string[] {
  const values: string[] = [];
  for (const [headerName, value] of headers) {
    if (headerName === name) values.push(value);
  }
  return values;
}
