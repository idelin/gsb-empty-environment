import { headersMatch, pathMatches } from "./conditions.ts";
import { ExactFraction } from "./fraction.ts";
import { prepareInput, resolveTableReference } from "./input.ts";
import type {
  CompiledPlan,
  CompiledResolution,
  CompiledRoute,
  CompileInput,
  ResolvedTarget,
  RouteRequest,
  SourceResolution,
} from "./types.ts";
import type {
  PreparedInput,
  PreparedMirror,
  PreparedTable,
} from "./input.ts";

export interface CompiledResolveOptions {
  includeShadowed?: boolean;
}

function fractionFromView(view: { numerator: string; denominator: string }): ExactFraction {
  return ExactFraction.from(BigInt(view.numerator), BigInt(view.denominator));
}

function resolvedTargets(route: CompiledRoute): ResolvedTarget[] {
  return route.targets.map((target) => {
    const fraction = fractionFromView(target.rawFraction).toView();
    return {
      service: target.service,
      weight: target.weight,
      weightText: target.weightText,
      fraction,
    };
  });
}

/**
 * First-match lookup against the already flattened plan. This is the compiled
 * behavior used when comparing requests before and after delegation.
 */
export function resolveCompiledRequest(
  plan: CompiledPlan,
  request: RouteRequest,
  options: CompiledResolveOptions = {},
): CompiledResolution | null {
  const routes = options.includeShadowed === true ? plan.routes : plan.liveRoutes;
  for (const route of routes) {
    if (
      pathMatches(route.path, request.path) &&
      headersMatch(route.headers, request)
    ) {
      return {
        route,
        targets: resolvedTargets(route),
        mirrors: [...route.mirrors],
      };
    }
  }
  return null;
}

export const matchCompiledRequest = resolveCompiledRequest;

interface SourceWalkContext {
  table: PreparedTable;
  sourceRuleIds: string[];
  mirrors: PreparedMirror[];
  stack: string[];
}

/**
 * Reference interpreter for the unexpanded tables. It is intentionally
 * deterministic: weighted delegate branches are tried in declaration order.
 * Use it in tests to compare first-match behavior with the flattened plan.
 */
export function resolveSourceRequest(
  input: CompileInput,
  request: RouteRequest,
  maxDelegateDepth = 64,
): SourceResolution | null {
  const prepared: PreparedInput = prepareInput(input);
  const root = prepared.tables.get(prepared.rootTableId);
  if (root === undefined) {
    return null;
  }

  const walk = (context: SourceWalkContext): SourceResolution | null => {
    if (context.stack.length > maxDelegateDepth) {
      return null;
    }

    for (const rule of context.table.rules) {
      if (!pathMatches(rule.path, request.path) || !headersMatch(rule.headers, request)) {
        continue;
      }

      const sourceRuleIds = [...context.sourceRuleIds, rule.id];
      const mirrors = [
        ...context.mirrors,
        ...rule.mirrors,
      ];

      if (rule.delegates !== undefined) {
        for (const delegate of rule.delegates) {
          const table = resolveTableReference(prepared, delegate.tableId);
          if (table === undefined || context.stack.includes(table.id)) {
            continue;
          }
          const result = walk({
            table,
            sourceRuleIds,
            mirrors,
            stack: [...context.stack, table.id],
          });
          if (result !== null) {
            return result;
          }
        }
        continue;
      }

      if (rule.targets === undefined) {
        continue;
      }

      return {
        tableId: context.table.id,
        sourceRuleIds,
        targetFractions: rule.targets.map((target) => ({
          service: target.service,
          fraction: target.fraction.toView(),
        })),
        mirrorServices: mirrors.map((mirror) => mirror.service),
      };
    }

    return null;
  };

  return walk({
    table: root,
    sourceRuleIds: [],
    mirrors: [],
    stack: [root.id],
  });
}
