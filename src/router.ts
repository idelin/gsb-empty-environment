import { Route } from "./route.ts";

export interface RouteMatch {
  route: Route;
  params: Record<string, string>;
}

function specificity(route: Route): readonly number[] {
  let statics = 0;
  let required = 0;
  let optional = 0;
  for (const segment of route.segments) {
    if (segment.kind === "static") statics += 1;
    else if (segment.optional) optional += 1;
    else required += 1;
  }
  return [statics, required, -optional, route.segments.length];
}

function compareSpecificity(a: Route, b: Route): number {
  const left = specificity(a);
  const right = specificity(b);
  for (let index = 0; index < left.length; index += 1) {
    const diff = right[index]! - left[index]!;
    if (diff !== 0) return diff;
  }
  return 0;
}

export class Router {
  private readonly routes: Route[] = [];

  add<P extends string>(route: Route<P> | P): Route<P> {
    const instance =
      typeof route === "string" ? new Route(route) : (route as Route<P>);
    this.routes.push(instance as Route);
    return instance;
  }

  get size(): number {
    return this.routes.length;
  }

  match(path: string): RouteMatch | null {
    const ordered = [...this.routes].sort(compareSpecificity);
    for (const route of ordered) {
      const params = route.match(path);
      if (params !== null) {
        return { route, params };
      }
    }
    return null;
  }
}
