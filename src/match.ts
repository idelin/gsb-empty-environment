import type { Variant } from "./pattern.ts";

export interface Priority {
  literalCount: number;
  omittedCount: number;
}

export function comparePriority(a: Priority, b: Priority): number {
  if (a.literalCount !== b.literalCount) return b.literalCount - a.literalCount;
  return a.omittedCount - b.omittedCount;
}

export type Params = Record<string, string | string[]>;

export function decodePath(rawPath: string): string[] | null {
  if (!rawPath.startsWith("/")) return null;
  if (rawPath === "/") return [];
  const rawSegments = rawPath.slice(1).split("/");
  const segments: string[] = [];
  for (const raw of rawSegments) {
    if (raw === "") return null;
    try {
      segments.push(decodeURIComponent(raw));
    } catch {
      return null;
    }
  }
  return segments;
}

export function matchVariant(variant: Variant, segments: string[]): Params | null {
  const params: Params = {};
  const matchers = variant.segments;
  let i = 0;
  for (; i < matchers.length; i++) {
    const matcher = matchers[i]!;
    if (matcher.kind === "wild") {
      params[matcher.name] = segments.slice(i);
      return params;
    }
    if (i >= segments.length) return null;
    const value = segments[i]!;
    if (matcher.kind === "lit") {
      if (value !== matcher.text) return null;
    } else {
      params[matcher.name] = value;
    }
  }
  return i === segments.length ? params : null;
}
