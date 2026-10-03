/** Deterministic pseudo-random URL dataset shared by the demo and tests. */

export function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const HOSTS = ["api.", "www.", "cdn.", "docs.", "static."];
const DOMAINS = ["example.com", "service.io", "data.dev", "shop.cn", "cloud.org"];
const PATHS = ["/v1/users", "/v2/orders", "/search", "/health", "/items/42", "/graphql"];

/** ~30% of rows are https://api.* URLs. */
export function generateUrls(count: number, seed = 20261004): string[] {
  const rand = mulberry32(seed);
  const urls = new Array<string>(count);
  for (let i = 0; i < count; i++) {
    const host = HOSTS[Math.floor(rand() * HOSTS.length)];
    const domain = DOMAINS[Math.floor(rand() * DOMAINS.length)];
    const path = PATHS[Math.floor(rand() * PATHS.length)];
    const scheme = rand() < 0.5 ? "https" : "http";
    urls[i] = `${scheme}://${host}${domain}${path}`;
  }
  return urls;
}
