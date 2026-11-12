import type {
  FetchInit,
  FetchLike,
  FetchResponse,
} from "../src/types.ts";

export interface StubResponse {
  readonly status?: number;
  readonly body?: string;
  readonly headers?: Record<string, string>;
}

export type StubRoute =
  | StubResponse
  | ((url: string, init: FetchInit) => StubResponse);

function responseFromStub(stub: StubResponse): FetchResponse {
  const headers = new Map<string, string>();
  for (const [name, value] of Object.entries(stub.headers ?? {})) {
    headers.set(name.toLowerCase(), value);
  }

  return {
    status: stub.status ?? 200,
    headers: {
      get(name: string): string | null {
        return headers.get(name.toLowerCase()) ?? null;
      },
    },
    async text(): Promise<string> {
      return stub.body ?? "";
    },
  };
}

export function createStubFetch(routes: Record<string, StubRoute>): {
  readonly fetch: FetchLike;
  readonly calls: string[];
} {
  const calls: string[] = [];

  const fetch: FetchLike = async (url, init) => {
    calls.push(url);

    let route = routes[url];
    if (route === undefined) {
      route = routes[url.split("?")[0] ?? url];
    }
    if (route === undefined) {
      return responseFromStub({ status: 404, body: "not found" });
    }

    const stub = typeof route === "function" ? route(url, init) : route;
    return responseFromStub(stub);
  };

  return { fetch, calls };
}

export function jrd(body: Record<string, unknown>): string {
  return JSON.stringify(body);
}

export function hostMeta(template: string, rel = "lrdd"): string {
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">',
    `<Link rel="${rel}" type="application/xrd+xml" template="${template}" />`,
    "</XRD>",
  ].join("");
}
