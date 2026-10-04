import { AttributeDomain, Request } from "./types";

export const MAX_DOMAIN_SIZE = 200_000;

export function enumerateDomain(domain: AttributeDomain): Request[] {
  const attributes = Object.keys(domain).sort();
  if (attributes.length === 0) {
    throw new Error("domain must declare at least one attribute");
  }
  let size = 1;
  for (const attr of attributes) {
    const values = domain[attr]!;
    if (values.length === 0) {
      throw new Error(`domain attribute "${attr}" has no values`);
    }
    size *= values.length;
    if (size > MAX_DOMAIN_SIZE) {
      throw new Error(
        `finite domain too large (${size} requests > ${MAX_DOMAIN_SIZE}); narrow the attribute values`
      );
    }
  }
  const requests: Request[] = new Array(size);
  const current: Record<string, string> = {};
  let cursor = 0;
  const walk = (depth: number): void => {
    if (depth === attributes.length) {
      requests[cursor++] = { ...current };
      return;
    }
    const attr = attributes[depth]!;
    for (const value of domain[attr]!) {
      current[attr] = value;
      walk(depth + 1);
    }
  };
  walk(0);
  return requests;
}
