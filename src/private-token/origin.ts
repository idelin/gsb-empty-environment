import { ChallengeParseError, OriginVerificationError } from "./errors.ts";

export interface HttpsOrigin {
  readonly scheme: "https";
  readonly host: string;
  readonly port: number | null;
  readonly serialized: string;
}

export interface RequestContext {
  readonly requestOrigin: string;
  readonly topLevelOrigin: string;
  readonly isTopLevelNavigation: boolean;
}

export function parseHttpsOrigin(raw: string): HttpsOrigin {
  let url: URL;
  try {
    url = new URL(raw);
  } catch (cause) {
    throw new ChallengeParseError(`Origin "${raw}" is not a valid URL`, { cause });
  }

  if (url.protocol !== "https:") {
    throw new ChallengeParseError(
      `Origin "${raw}" must use the https scheme; got ${url.protocol}`,
    );
  }
  if (url.username !== "" || url.password !== "") {
    throw new ChallengeParseError(`Origin "${raw}" must not contain userinfo`);
  }
  if (url.pathname !== "" && url.pathname !== "/") {
    throw new ChallengeParseError(`Origin "${raw}" must not contain a path`);
  }
  if (url.search !== "" || url.hash !== "") {
    throw new ChallengeParseError(`Origin "${raw}" must not contain a query or fragment`);
  }

  const host = url.hostname;
  const port = url.port === "" ? null : Number(url.port);
  const serialized = port === null ? `https://${host}` : `https://${host}:${port}`;

  return { scheme: "https", host, port, serialized };
}

export function verifyChallengeOrigin(
  originInfo: readonly string[],
  context: RequestContext,
): void {
  const request = parseRequestOrigin(context.requestOrigin);

  if (originInfo.length > 0) {
    const allowed = new Set(originInfo.map((entry) => parseHttpsOrigin(entry).serialized));
    if (!allowed.has(request.serialized)) {
      throw new OriginVerificationError(
        `Request origin ${request.serialized} is not listed in the challenge origin_info`,
      );
    }
    return;
  }

  if (!context.isTopLevelNavigation) {
    throw new OriginVerificationError(
      "Challenge with empty origin_info is only valid for top-level navigation requests",
    );
  }

  const topLevel = parseRequestOrigin(context.topLevelOrigin);
  if (topLevel.serialized !== request.serialized) {
    throw new OriginVerificationError(
      "Challenge with empty origin_info requires the request to be same-origin with the top-level document",
    );
  }
}

function parseRequestOrigin(raw: string): HttpsOrigin {
  try {
    return parseHttpsOrigin(raw);
  } catch (cause) {
    if (cause instanceof ChallengeParseError) {
      throw new OriginVerificationError(
        `Request origin "${raw}" must be an https origin`,
        { cause },
      );
    }
    throw cause;
  }
}
