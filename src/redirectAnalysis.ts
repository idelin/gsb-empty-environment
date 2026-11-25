import type {
  AndroidManifest,
  ChainHop,
  ComponentInfo,
  ComponentSurface,
  RedirectChain,
  RedirectEdge,
  RedirectOnlyTarget,
  Severity,
} from './types.ts';
import { isSignatureLevel, type PermissionResolver } from './exportSurface.ts';

interface TraversalNode {
  name: string;
  path: string[];
  hops: ChainHop[];
  blockedBy?: string;
}

function edgeKey(edge: RedirectEdge): string {
  return `${edge.source}->${edge.target}(${edge.kind})`;
}

function chainSeverity(
  target: ComponentInfo,
  hops: ChainHop[],
  entryAccessible: boolean,
  uriGrantBypass: boolean,
): Severity {
  if (!entryAccessible) {
    return 'info';
  }
  if (target.type === 'provider' && uriGrantBypass) {
    return 'high';
  }
  if (target.type === 'activity' || target.type === 'activity-alias') {
    return hops.some((hop) => hop.intentPassthrough) ? 'high' : 'medium';
  }
  if (target.type === 'service') {
    return 'high';
  }
  if (target.type === 'receiver') {
    return 'medium';
  }
  return 'medium';
}

export interface RedirectAnalysisResult {
  chains: RedirectChain[];
  redirectOnly: RedirectOnlyTarget[];
  surfaces: Array<Pick<ComponentSurface, 'name' | 'reachableViaRedirect' | 'redirectEntries'>>;
}

export function analyzeRedirects(
  manifest: AndroidManifest,
  surfaces: ComponentSurface[],
  resolver: PermissionResolver,
  extraRedirects: RedirectEdge[] = [],
  maxHops = 8,
): RedirectAnalysisResult {
  const byName = new Map<string, ComponentInfo>();
  for (const component of manifest.components) {
    byName.set(component.name, component);
  }

  const edges: RedirectEdge[] = [];
  const seenEdgeKeys = new Set<string>();
  const addEdge = (edge: RedirectEdge): void => {
    const key = edgeKey(edge);
    if (seenEdgeKeys.has(key)) {
      return;
    }
    seenEdgeKeys.add(key);
    edges.push(edge);
  };

  for (const component of manifest.components) {
    if (component.type === 'activity-alias' && component.targetActivity) {
      addEdge({
        source: component.name,
        target: component.targetActivity,
        kind: 'activity-alias',
        intentPassthrough: true,
        note: 'manifest activity-alias forwards launches to targetActivity',
      });
    }
  }

  for (const edge of extraRedirects) {
    addEdge(edge);
  }

  const adjacency = new Map<string, RedirectEdge[]>();
  for (const edge of edges) {
    const outgoing = adjacency.get(edge.source);
    if (outgoing) {
      outgoing.push(edge);
    } else {
      adjacency.set(edge.source, [edge]);
    }
  }
  for (const outgoing of adjacency.values()) {
    outgoing.sort((left, right) => left.target.localeCompare(right.target));
  }

  const surfaceByName = new Map(surfaces.map((surface) => [surface.name, surface]));
  const entryComponents = surfaces.filter((surface) => surface.exported);
  const chains: RedirectChain[] = [];
  const reachable = new Map<
    string,
    { entries: Set<string>; hopCount: number }
  >();

  for (const entry of entryComponents) {
    const entryProtection = entry.protection;
    const entryAccessible =
      !entryProtection.permission || !isSignatureLevel(entryProtection.level);

    const queue: TraversalNode[] = [
      {
        name: entry.name,
        path: [entry.name],
        hops: [],
      },
    ];

    while (queue.length > 0) {
      const current = queue.shift()!;
      if (current.hops.length >= maxHops) {
        continue;
      }

      const outgoing = adjacency.get(current.name) ?? [];
      for (const edge of outgoing) {
        const target = byName.get(edge.target);
        if (!target) {
          continue;
        }
        if (current.path.includes(edge.target)) {
          continue;
        }

        const hop: ChainHop = {
          from: edge.source,
          to: edge.target,
          kind: edge.kind,
          intentPassthrough: edge.intentPassthrough ?? false,
        };
        if (edge.requiresPermission) {
          hop.requiresPermission = edge.requiresPermission;
        }

        const hopPermission = edge.requiresPermission
          ? resolver.resolve(edge.requiresPermission)
          : undefined;
        const edgeBlocked =
          hopPermission?.permission && isSignatureLevel(hopPermission.level)
            ? hopPermission.permission
            : undefined;

        const nextPath = [...current.path, edge.target];
        const nextHops = [...current.hops, hop];
        const blockedBy = current.blockedBy ?? edgeBlocked;
        const targetSurface = surfaceByName.get(edge.target);
        const targetExported = targetSurface?.exported ?? false;
        const uriGrantBypass =
          target.type === 'provider' && target.grantUriPermissions === true;
        const exploitable = entryAccessible && blockedBy === undefined;

        if (!targetExported) {
          const chain: RedirectChain = {
            entry: entry.name,
            target: edge.target,
            targetExported: false,
            hops: nextHops,
            hopCount: nextHops.length,
            entryAccessible,
            exploitable,
            intentPassthrough: nextHops.some((item) => item.intentPassthrough),
            uriGrantBypass,
            severity: chainSeverity(target, nextHops, entryAccessible, uriGrantBypass),
          };
          if (blockedBy) {
            chain.blockedBy = blockedBy;
          }
          chains.push(chain);

          const prior = reachable.get(edge.target);
          if (prior) {
            prior.entries.add(entry.name);
            prior.hopCount = Math.min(prior.hopCount, nextHops.length);
          } else {
            reachable.set(edge.target, {
              entries: new Set([entry.name]),
              hopCount: nextHops.length,
            });
          }
        }

        queue.push({
          name: edge.target,
          path: nextPath,
          hops: nextHops,
          blockedBy,
        });
      }
    }
  }

  const patch = new Map<string, ComponentSurface['redirectEntries']>();
  for (const [targetName, info] of reachable) {
    patch.set(targetName, [...info.entries].sort());
  }
  for (const surface of surfaces) {
    const entries = patch.get(surface.name);
    if (entries) {
      surface.reachableViaRedirect = true;
      surface.redirectEntries = entries;
    }
  }

  const redirectOnly: RedirectOnlyTarget[] = [];
  for (const [targetName, info] of reachable) {
    const target = byName.get(targetName);
    const targetSurface = surfaceByName.get(targetName);
    if (!target || !targetSurface || targetSurface.exported) {
      continue;
    }
    const targetChains = chains.filter(
      (chain) => chain.target === targetName,
    );
    const entries = targetChains
      .map((chain) => ({
        entry: chain.entry,
        hopCount: chain.hopCount,
        exploitable: chain.exploitable,
        severity: chain.severity,
      }))
      .sort((left, right) => left.hopCount - right.hopCount);
    const shortest = entries.reduce(
      (minimum, item) => Math.min(minimum, item.hopCount),
      Number.POSITIVE_INFINITY,
    );
    redirectOnly.push({
      name: targetName,
      type: target.type,
      shortestHopCount: shortest,
      exploitable: entries.some((item) => item.exploitable),
      entries,
    });
  }
  redirectOnly.sort((left, right) => left.name.localeCompare(right.name));
  chains.sort(
    (left, right) =>
      left.entry.localeCompare(right.entry) ||
      left.hopCount - right.hopCount ||
      left.target.localeCompare(right.target),
  );

  return {
    chains,
    redirectOnly,
    surfaces: surfaces.map((surface) => ({
      name: surface.name,
      reachableViaRedirect: surface.reachableViaRedirect,
      redirectEntries: surface.redirectEntries,
    })),
  };
}
