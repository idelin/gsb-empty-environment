import { parsePattern, expand, type Pattern, type Variant } from "./pattern.ts";
import { comparePriority, decodePath, matchVariant, type Params, type Priority } from "./match.ts";
import { findConflicts, type CompiledRoute, type Conflict } from "./conflict.ts";

export interface Route<T> extends CompiledRoute {
  pattern: Pattern;
  data: T | undefined;
}

export interface Match<T> extends Priority {
  route: Route<T>;
  data: T | undefined;
  params: Params;
}

export interface RouteDef<T> {
  path: string;
  data?: T;
}

export class ConflictError extends Error {
  readonly conflicts: Conflict[];
  constructor(conflicts: Conflict[]) {
    super(
      "route conflicts detected:\n" +
        conflicts
          .map(
            (c) =>
              `  #${c.routes[0]} ${JSON.stringify(c.sources[0])} vs #${c.routes[1]} ` +
              `${JSON.stringify(c.sources[1])} tie at ` +
              `(literals=${c.priority.literalCount}, omitted=${c.priority.omittedCount}), ` +
              `witness ${c.witness}`,
          )
          .join("\n"),
    );
    this.name = "ConflictError";
    this.conflicts = conflicts;
  }
}

export class Router<T = unknown> {
  readonly routes: Route<T>[] = [];

  constructor(defs: readonly RouteDef<T>[] = []) {
    for (const def of defs) {
      this.add(def.path, def.data as T);
    }
  }

  add(path: string, data?: T): this {
    const pattern = parsePattern(path);
    const route: Route<T> = {
      source: path,
      pattern,
      variants: expand(pattern),
      data,
      index: this.routes.length,
    };
    this.routes.push(route);
    return this;
  }

  match(rawPath: string): Match<T> | null {
    return this.matchAll(rawPath)[0] ?? null;
  }

  matchAll(rawPath: string): Match<T>[] {
    const segments = decodePath(rawPath);
    if (segments === null) return [];
    const matches: Match<T>[] = [];
    for (const route of this.routes) {
      let best: Match<T> | null = null;
      for (const variant of route.variants) {
        const params = matchVariant(variant, segments);
        if (params === null) continue;
        const candidate: Match<T> = {
          route,
          data: route.data,
          params,
          literalCount: variant.literalCount,
          omittedCount: variant.omittedCount,
        };
        if (best === null || comparePriority(candidate, best) < 0) best = candidate;
      }
      if (best) matches.push(best);
    }
    // Array.prototype.sort is stable, so equal priorities keep registration order.
    matches.sort(comparePriority);
    return matches;
  }

  conflicts(): Conflict[] {
    return findConflicts(this.routes);
  }

  static compile<T>(defs: readonly RouteDef<T>[]): Router<T> {
    const router = new Router<T>(defs);
    const conflicts = router.conflicts();
    if (conflicts.length > 0) throw new ConflictError(conflicts);
    return router;
  }
}
