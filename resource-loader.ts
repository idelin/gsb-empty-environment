import {
  COEPPolicyError,
  adjudicateResponse,
  type CrossOriginEmbedderPolicy,
  type CrossOriginResourcePolicy,
  type RequestMode,
  type ResponsePolicyContext,
  type ResponsePolicyDecision,
} from "./index.ts";

export interface ResourceLoaderOptions {
  coep: CrossOriginEmbedderPolicy | string;
  origin?: string | URL;
  requestOrigin?: string | URL;
  fetch?: typeof fetch;
  corp?: CrossOriginResourcePolicy | string | null;
  crossOrigin?: boolean;
  sameSite?: boolean;
  mode?: RequestMode | string;
}

export interface ResourceLoadOptions extends Omit<RequestInit, "mode"> {
  coep?: CrossOriginEmbedderPolicy | string;
  origin?: string | URL;
  requestOrigin?: string | URL;
  fetch?: typeof fetch;
  corp?: CrossOriginResourcePolicy | string | null;
  crossOrigin?: boolean;
  sameSite?: boolean;
  mode?: string;
}

function originOf(value: string | URL | undefined): string | null {
  if (value === undefined) return null;

  try {
    return new URL(value.toString()).origin;
  } catch {
    return null;
  }
}

function requestOriginOf(input: RequestInfo | URL): string | null {
  const value =
    typeof input === "string"
      ? input
      : input instanceof URL
        ? input.href
        : input.url;

  try {
    return new URL(value).origin;
  } catch {
    return null;
  }
}

function isCrossOrigin(
  input: RequestInfo | URL,
  origin: string | null,
  explicit: boolean | undefined,
): boolean {
  if (typeof explicit === "boolean") return explicit;

  const targetOrigin = requestOriginOf(input);

  if (origin !== null && targetOrigin !== null) {
    return origin !== targetOrigin;
  }

  if (targetOrigin === null) return false;

  return true;
}

function responseCorp(response: Response): string | null {
  try {
    return response.headers.get("Cross-Origin-Resource-Policy");
  } catch {
    return null;
  }
}

function fetchInit(init: ResourceLoadOptions): RequestInit {
  const {
    coep: _coep,
    origin: _origin,
    requestOrigin: _requestOrigin,
    fetch: _fetch,
    corp: _corp,
    crossOrigin: _crossOrigin,
    sameSite: _sameSite,
    ...requestInit
  } = init;

  return requestInit as RequestInit;
}

function normalizedToken(value: unknown): string {
  return String(value).trim().toLowerCase().replaceAll("_", "-");
}

function isCredentialless(coep: unknown): boolean {
  return normalizedToken(coep) === "credentialless";
}

function isNoCors(mode: unknown): boolean {
  return normalizedToken(mode) === "no-cors";
}

type LoaderOrigin = string | URL | Partial<ResourceLoaderOptions>;

export class COEPResourceLoader {
  readonly coep: CrossOriginEmbedderPolicy | string;
  readonly origin: string | null;
  readonly corp: CrossOriginResourcePolicy | string | null;
  readonly crossOrigin: boolean | undefined;
  readonly sameSite: boolean | undefined;
  readonly mode: RequestMode | string;
  readonly #fetch: typeof fetch | undefined;

  constructor(
    options: ResourceLoaderOptions | CrossOriginEmbedderPolicy | string,
    origin?: LoaderOrigin,
  ) {
    let config: ResourceLoaderOptions;

    if (typeof options === "string") {
      config =
        typeof origin === "object" && origin !== null && !(origin instanceof URL)
          ? { ...origin, coep: options }
          : {
              coep: options,
              origin: origin as string | URL | undefined,
            };
    } else {
      config = options;
    }

    this.coep = config.coep;
    this.origin =
      originOf(config.requestOrigin) ?? originOf(config.origin);
    this.corp = config.corp ?? null;
    this.crossOrigin = config.crossOrigin;
    this.sameSite = config.sameSite;
    this.mode = config.mode ?? "no-cors";
    this.#fetch = config.fetch;
  }

  async load(
    input: RequestInfo | URL,
    init: ResourceLoadOptions = {},
  ): Promise<Response> {
    const coep = init.coep ?? this.coep;
    const origin =
      originOf(init.requestOrigin) ??
      originOf(init.origin) ??
      this.origin;
    const crossOrigin = isCrossOrigin(
      input,
      origin,
      init.crossOrigin ?? this.crossOrigin,
    );
    const mode = init.mode ?? this.mode;
    const requestInit = fetchInit(init);

    if (
      crossOrigin &&
      isNoCors(mode) &&
      isCredentialless(coep)
    ) {
      requestInit.credentials = "omit";
    }

    const fetchImpl = init.fetch ?? this.#fetch ?? globalThis.fetch;

    if (typeof fetchImpl !== "function") {
      throw new TypeError("A fetch implementation is required");
    }

    const response = await fetchImpl(input, requestInit);
    const result = adjudicateResponse({
      coep,
      corp: responseCorp(response) ?? init.corp ?? this.corp ?? null,
      crossOrigin,
      sameSite: init.sameSite ?? this.sameSite,
      mode,
      response,
    } satisfies ResponsePolicyContext);

    if (result.blocked) {
      throw new COEPPolicyError(result.reason, result);
    }

    return result.response ?? response;
  }
}

export function createResourceLoader(
  options: ResourceLoaderOptions | CrossOriginEmbedderPolicy | string,
  origin?: LoaderOrigin,
): COEPResourceLoader {
  return new COEPResourceLoader(options, origin);
}

export { COEPResourceLoader as ResourceLoader };

export async function loadResource(
  input: RequestInfo | URL,
  options: ResourceLoadOptions | CrossOriginEmbedderPolicy | string,
): Promise<Response> {
  if (typeof options === "string") {
    return loadResource(input, { coep: options });
  }

  const loader = new COEPResourceLoader({
    coep: options.coep ?? "unsafe-none",
    origin: options.origin,
    requestOrigin: options.requestOrigin,
    fetch: options.fetch,
    corp: options.corp,
    crossOrigin: options.crossOrigin,
    sameSite: options.sameSite,
    mode: options.mode,
  });

  return loader.load(input, options);
}

export const load = loadResource;

export type { ResponsePolicyDecision };
export { adjudicateResponse } from "./index.ts";
export { COEPPolicyError } from "./index.ts";

export default COEPResourceLoader;
