import {
  intersectPaths,
  mergeHeaderSets,
} from "./conditions.ts";
import { findShadowSources } from "./coverage.ts";
import { ExactFraction, normalizeFractions } from "./fraction.ts";
import {
  prepareInput,
  resolveTableReference,
  RouteCompileError,
} from "./input.ts";
import type {
  CompiledMirror,
  CompiledPlan,
  CompiledRoute,
  CompiledTarget,
  CompileIssue,
  CompileIssueCode,
  CompileOptions,
  CompileInput,
  DeadRuleReport,
  DelegationCycle,
  FractionView,
  HeaderCondition,
  MirrorCycle,
  PathCondition,
  PrunedBranch,
  RouteSourceRef,
} from "./types.ts";
import type {
  PreparedDelegate,
  PreparedInput,
  PreparedMirror,
  PreparedRule,
  PreparedTable,
  PreparedTarget,
} from "./input.ts";

interface ActiveMirror extends PreparedMirror {
  sourceRuleIds: string[];
}

interface RawRoute {
  baseId: string;
  id: string;
  sourceRuleIds: string[];
  sourceChain: RouteSourceRef[];
  path: PathCondition;
  headers: HeaderCondition[];
  targets: PreparedTarget[];
  mirrors: ActiveMirror[];
  branchFraction: ExactFraction;
  shadowedBy: string[] | null;
}

interface ExpandContext {
  branchFraction: ExactFraction;
  path: PathCondition;
  headers: HeaderCondition[];
  mirrors: ActiveMirror[];
  stack: string[];
  sourceChain: RouteSourceRef[];
}

interface MirrorEdge {
  to: string;
  rate: ExactFraction;
  sourceRuleIds: string[];
}

function issue(
  issues: CompileIssue[],
  code: CompileIssueCode,
  message: string,
  sourceRuleIds: readonly string[],
  tableId?: string,
  severity: "error" | "warning" = "error",
): void {
  issues.push({
    code,
    severity,
    message,
    sourceRuleIds: [...sourceRuleIds],
    ...(tableId === undefined ? {} : { tableId }),
  });
}

function tableIdForChain(chain: readonly RouteSourceRef[]): string | undefined {
  return chain.length === 0 ? undefined : chain[chain.length - 1].tableId;
}

function addPruned(
  pruned: PrunedBranch[],
  sourceRuleIds: readonly string[],
  reason: PrunedBranch["reason"],
  detail: string,
): void {
  pruned.push({ sourceRuleIds: [...sourceRuleIds], reason, detail });
}

function canonicalCycle(nodesWithRepeat: readonly string[]): string[] {
  const nodes = nodesWithRepeat.slice(0, -1);
  if (nodes.length === 0) {
    return [...nodesWithRepeat];
  }

  let smallest = 0;
  for (let index = 1; index < nodes.length; index += 1) {
    if (nodes[index].localeCompare(nodes[smallest]) < 0) {
      smallest = index;
    }
  }
  return [
    ...nodes.slice(smallest),
    ...nodes.slice(0, smallest),
    nodes[smallest],
  ];
}

function cycleKey(nodesWithRepeat: readonly string[]): string {
  return canonicalCycle(nodesWithRepeat).slice(0, -1).join("\u0000");
}

function fractionOf(value: ExactFraction): FractionView {
  return value.toView();
}

function bigintToSafeNumber(value: bigint, label: string): number {
  const numberValue = Number(value);
  if (!Number.isSafeInteger(numberValue)) {
    throw new RouteCompileError(
      "invalid-weight",
      `${label} exceeds JavaScript's safe integer range`,
    );
  }
  return numberValue;
}

function buildRoute(
  rule: PreparedRule,
  terminalTable: PreparedTable,
  context: ExpandContext,
): RawRoute {
  if (rule.targets === undefined) {
    throw new RouteCompileError("invalid-rule", `rule "${rule.id}" has no targets`);
  }
  const terminalRule = context.sourceChain[context.sourceChain.length - 1];
  if (terminalRule === undefined) {
    throw new RouteCompileError("invalid-rule", "terminal route has no source rule");
  }

  return {
    baseId: `${terminalTable.id}:${terminalRule.ruleId}`,
    id: "",
    sourceRuleIds: [...context.sourceChain.map((source) => source.ruleId)],
    sourceChain: [...context.sourceChain],
    path: context.path,
    headers: context.headers,
    targets: [...rule.targets],
    mirrors: [...context.mirrors],
    branchFraction: context.branchFraction,
    shadowedBy: null,
  };
}

function materializeRoute(
  raw: RawRoute,
  routeId: string,
): CompiledRoute {
  const exactTargets = raw.targets.map((target) => ({
    service: target.service,
    inputFraction: target.fraction,
    rawFraction: raw.branchFraction.multiply(target.fraction),
  }));
  const normalized = normalizeFractions(exactTargets.map((target) => target.rawFraction));

  const targets: CompiledTarget[] = exactTargets.map((target, index) => {
    const weight = normalized.weights[index];
    return {
      service: target.service,
      inputFraction: fractionOf(target.inputFraction),
      rawFraction: fractionOf(target.rawFraction),
      weight: bigintToSafeNumber(weight, "normalized weight"),
      weightText: weight.toString(),
    };
  });

  const mirrors: CompiledMirror[] = raw.mirrors.map((mirror) => ({
    service: mirror.service,
    sampleRate: fractionOf(mirror.sampleRate),
    sourceRuleIds: [...mirror.sourceRuleIds],
  }));

  return {
    id: routeId,
    sourceRuleIds: raw.sourceRuleIds,
    sourceChain: raw.sourceChain,
    path: raw.path,
    headers: raw.headers,
    targets,
    totalWeight: bigintToSafeNumber(normalized.total, "total weight"),
    totalWeightText: normalized.total.toString(),
    branchFraction: fractionOf(raw.branchFraction),
    mirrors,
    shadowedBy: raw.shadowedBy,
  };
}

function routeMatchesShadowShape(route: CompiledRoute): {
  id: string;
  sourceRuleIds: string[];
  path: PathCondition;
  headers: HeaderCondition[];
} {
  return {
    id: route.id,
    sourceRuleIds: route.sourceRuleIds,
    path: route.path,
    headers: route.headers,
  };
}

function assignRouteIds(rawRoutes: readonly RawRoute[]): string[] {
  const totals = new Map<string, number>();
  for (const route of rawRoutes) {
    totals.set(route.baseId, (totals.get(route.baseId) ?? 0) + 1);
  }

  const used = new Map<string, number>();
  return rawRoutes.map((route) => {
    if ((totals.get(route.baseId) ?? 0) === 1) {
      return route.baseId;
    }
    const next = (used.get(route.baseId) ?? 0) + 1;
    used.set(route.baseId, next);
    return `${route.baseId}~${next}`;
  });
}

function analyzeShadows(
  routes: CompiledRoute[],
  deadRules: DeadRuleReport[],
): void {
  const earlierLive: Array<ReturnType<typeof routeMatchesShadowShape>> = [];

  for (const route of routes) {
    const shadow = findShadowSources(
      { path: route.path, headers: route.headers },
      earlierLive,
    );
    if (shadow !== null) {
      route.shadowedBy = shadow.routeIds;
      deadRules.push({
        routeId: route.id,
        sourceRuleIds: route.sourceRuleIds,
        shadowedBy: shadow.routeIds,
        shadowSourceRuleIds: shadow.sourceRuleIds,
      });
      continue;
    }
    earlierLive.push(routeMatchesShadowShape(route));
  }
}

function addMirrorEdge(
  graph: Map<string, Map<string, MirrorEdge>>,
  from: string,
  to: string,
  rate: ExactFraction,
  sourceRuleIds: readonly string[],
): void {
  let outgoing = graph.get(from);
  if (outgoing === undefined) {
    outgoing = new Map<string, MirrorEdge>();
    graph.set(from, outgoing);
  }

  const existing = outgoing.get(to);
  if (existing === undefined) {
    outgoing.set(to, { to, rate, sourceRuleIds: [...sourceRuleIds] });
  } else {
    outgoing.set(to, {
      to,
      rate: existing.rate.add(rate),
      sourceRuleIds: [...new Set([...existing.sourceRuleIds, ...sourceRuleIds])],
    });
  }
}

function analyzeMirrorCycles(
  routes: readonly CompiledRoute[],
  issues: CompileIssue[],
): MirrorCycle[] {
  const graph = new Map<string, Map<string, MirrorEdge>>();

  for (const route of routes) {
    for (const target of route.targets) {
      for (const mirror of route.mirrors) {
        addMirrorEdge(
          graph,
          target.service,
          mirror.service,
          parseMirrorSample(mirror),
          mirror.sourceRuleIds,
        );
      }
    }
  }

  const nodes = [...graph.keys()].sort();
  const cycles: MirrorCycle[] = [];
  const seen = new Set<string>();

  const visit = (
    start: string,
    node: string,
    nodePath: string[],
    edgeRates: ExactFraction[],
    sourceRuleIds: string[],
  ): void => {
    const outgoing = graph.get(node);
    if (outgoing === undefined) {
      return;
    }

    for (const edge of outgoing.values()) {
      const nextPath = [...nodePath, edge.to];
      const nextRates = [...edgeRates, edge.rate];
      const nextSources = [...sourceRuleIds, ...edge.sourceRuleIds];
      if (edge.to === start) {
        const key = cycleKey(nextPath);
        if (seen.has(key)) {
          continue;
        }
        seen.add(key);

        const gain = nextRates.reduce(
          (product, rate) => product.multiply(rate),
          ExactFraction.one(),
        );
        cycles.push({
          services: nextPath,
          sourceRuleIds: [...new Set(nextSources)],
          sampleGain: gain.toView(),
          amplifying: gain.greaterThanOrEqual(ExactFraction.one()),
        });
      } else if (!nodePath.includes(edge.to)) {
        visit(start, edge.to, nextPath, nextRates, nextSources);
      }
    }
  };

  for (const start of nodes) {
    visit(start, start, [start], [], []);
    if (cycles.length > 2000) {
      throw new RouteCompileError(
        "invalid-rule",
        "mirror amplification graph has too many simple cycles",
      );
    }
  }

  cycles.sort((left, right) =>
    left.services.join("\u0000").localeCompare(right.services.join("\u0000")),
  );

  for (const cycle of cycles) {
    const pathText = cycle.services.join(" -> ");
    const severity = cycle.amplifying ? "error" : "warning";
    issue(
      issues,
      "mirror-amplification-cycle",
      `mirror feedback loop detected (${pathText}); aggregate sample gain ${cycle.sampleGain.text}`,
      cycle.sourceRuleIds,
      undefined,
      severity,
    );
  }

  return cycles;
}

function parseMirrorSample(mirror: CompiledMirror): ExactFraction {
  return ExactFraction.from(
    BigInt(mirror.sampleRate.numerator),
    BigInt(mirror.sampleRate.denominator),
  );
}

/**
 * Compiles a root table and named child tables into an ordered, flattened
 * routing plan. Configuration cycles are reported and their branches are
 * pruned rather than followed.
 */
export function compileRouteTables(
  input: CompileInput,
  options: CompileOptions = {},
): CompiledPlan {
  const prepared: PreparedInput = prepareInput(input);
  const maxDepth = options.maxDelegateDepth ?? 64;

  const rawRoutes: RawRoute[] = [];
  const prunedBranches: PrunedBranch[] = [];
  const issues: CompileIssue[] = [];
  const delegateCycles: DelegationCycle[] = [];
  const cycleKeys = new Set<string>();

  const root = prepared.tables.get(prepared.rootTableId);
  if (root === undefined) {
    throw new RouteCompileError("invalid-rule", "root table was not prepared");
  }

  const recordDelegationCycle = (
    cycle: readonly string[],
    sourceRuleIds: readonly string[],
  ): void => {
    const key = cycleKey(cycle);
    if (cycleKeys.has(key)) {
      return;
    }
    cycleKeys.add(key);
    const canonical = canonicalCycle(cycle);
    delegateCycles.push({
      tables: canonical,
      sourceRuleIds: [...sourceRuleIds],
    });
    issue(
      issues,
      "delegate-cycle",
      `delegation cycle detected: ${canonical.join(" -> ")}`,
      sourceRuleIds,
      canonical[0],
    );
  };

  const expand = (
    table: PreparedTable,
    context: ExpandContext,
  ): void => {
    if (context.stack.length > maxDepth) {
      throw new RouteCompileError(
        "invalid-rule",
        `delegate depth exceeds maximum of ${maxDepth}`,
        context.sourceChain.map((source) => source.ruleId),
      );
    }

    for (const rule of table.rules) {
      const sourceRef: RouteSourceRef = {
        tableId: table.id,
        ruleId: rule.id,
        index: rule.index,
      };
      const sourceChain = [...context.sourceChain, sourceRef];
      const sourceRuleIds = sourceChain.map((source) => source.ruleId);

      const paths = intersectPaths(context.path, rule.path);
      if (paths === null) {
        addPruned(
          prunedBranches,
          sourceRuleIds,
          "path",
          `path ${rule.path.kind}:${rule.path.value} does not overlap ${context.path.kind}:${context.path.value}`,
        );
        issue(
          issues,
          "contradictory-path",
          `path conditions do not overlap at rule "${rule.id}"`,
          sourceRuleIds,
          table.id,
        );
        continue;
      }

      const headers = mergeHeaderSets(context.headers, rule.headers);
      if (headers === null) {
        addPruned(
          prunedBranches,
          sourceRuleIds,
          "header",
          "header assertions are contradictory",
        );
        issue(
          issues,
          "contradictory-header",
          `header conditions are contradictory at rule "${rule.id}"`,
          sourceRuleIds,
          table.id,
        );
        continue;
      }

      const mirrors: ActiveMirror[] = [
        ...context.mirrors,
        ...rule.mirrors.map((mirror) => ({
          ...mirror,
          sourceRuleIds,
        })),
      ];

      if (rule.delegates !== undefined) {
        for (const delegate of rule.delegates) {
          const targetTable = resolveTableReference(prepared, delegate.tableId);
          if (targetTable === undefined) {
            addPruned(
              prunedBranches,
              sourceRuleIds,
              "missing-table",
              `delegated table "${delegate.tableId}" does not exist`,
            );
            issue(
              issues,
              "missing-table",
              `delegated table "${delegate.tableId}" does not exist`,
              sourceRuleIds,
              table.id,
            );
            continue;
          }

          const cycleStart = context.stack.indexOf(targetTable.id);
          if (cycleStart >= 0) {
            const cycle = [...context.stack.slice(cycleStart), targetTable.id];
            addPruned(
              prunedBranches,
              sourceRuleIds,
              "delegate-cycle",
              `delegated table "${targetTable.id}" is already on the expansion stack`,
            );
            recordDelegationCycle(cycle, sourceRuleIds);
            continue;
          }

          if (delegate.fraction.isZero()) {
            continue;
          }

          expand(targetTable, {
            branchFraction: context.branchFraction.multiply(delegate.fraction),
            path: paths,
            headers,
            mirrors,
            stack: [...context.stack, targetTable.id],
            sourceChain,
          });
        }
        continue;
      }

      if (rule.targets === undefined) {
        throw new RouteCompileError(
          "invalid-rule",
          `rule "${rule.id}" has neither targets nor a delegate`,
          sourceRuleIds,
        );
      }

      rawRoutes.push(buildRoute(rule, table, {
        branchFraction: context.branchFraction,
        path: paths,
        headers,
        mirrors,
        stack: context.stack,
        sourceChain,
      }));
    }
  };

  expand(root, {
    branchFraction: ExactFraction.one(),
    path: { kind: "prefix", value: "/" },
    headers: [],
    mirrors: [],
    stack: [root.id],
    sourceChain: [],
  });

  const routeIds = assignRouteIds(rawRoutes);
  const routes = rawRoutes.map((route, index) => materializeRoute(route, routeIds[index]));
  const deadRules: DeadRuleReport[] = [];
  analyzeShadows(routes, deadRules);

  const mirrorCycles = analyzeMirrorCycles(
    routes.filter((route) => route.shadowedBy === null),
    issues,
  );

  return {
    rootTableId: prepared.rootTableId,
    routes,
    liveRoutes: routes.filter((route) => route.shadowedBy === null),
    deadRules,
    delegateCycles,
    mirrorCycles,
    prunedBranches,
    issues,
    tableIds: prepared.tableIds,
  };
}

export { RouteCompileError };
