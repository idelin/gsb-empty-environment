import { BootstrapRegistry } from '../src/bootstrap.ts';
import type { FetchLike, FetchResponse } from '../src/admission.ts';

export const BOOTSTRAP_DOC = {
  version: '1.0',
  publication: '2026-11-19T00:00:00Z',
  services: [
    [['de'], ['https://rdap.denic.de/']],
    [['com', 'net'], ['https://rdap.verisign.com/']],
  ],
};

export function makeRegistry(): BootstrapRegistry {
  return BootstrapRegistry.fromIANA(BOOTSTRAP_DOC);
}

export function domainBody(
  ldhName: string,
  extra: Record<string, unknown> = {},
): string {
  return JSON.stringify({
    objectClassName: 'domain',
    rdapConformance: ['rdap_level_0'],
    ldhName,
    links: [],
    ...extra,
  });
}

export interface MockFetch {
  fetch: FetchLike;
  calls: string[];
}

export function makeFetch(
  routes: Record<string, FetchResponse | ((url: string) => FetchResponse)>,
): MockFetch {
  const calls: string[] = [];
  const fetch: FetchLike = async (url: string) => {
    calls.push(url);
    const route = routes[url];
    if (!route) throw new Error(`unexpected fetch: ${url}`);
    return typeof route === 'function' ? route(url) : route;
  };
  return { fetch, calls };
}

export function ok(body: string): FetchResponse {
  return { status: 200, headers: {}, body };
}

export function redirect(location: string): FetchResponse {
  return { status: 302, headers: { location }, body: '' };
}

export const PUBLIC_RESOLVER = async (): Promise<string[]> => ['93.184.216.34'];
