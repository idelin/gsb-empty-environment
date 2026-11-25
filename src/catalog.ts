import type {
  PurposeCatalog,
  PurposeNodeId,
  RegionCatalog,
  RegionCode,
} from "./types.ts";

/** Ancestor path of a node, including the node itself, root first. */
export function purposePath(
  catalog: PurposeCatalog,
  node: PurposeNodeId,
): PurposeNodeId[] {
  const path: PurposeNodeId[] = [];
  let current: PurposeNodeId | null = node;
  const guard = new Set<PurposeNodeId>();
  while (current !== null) {
    if (guard.has(current)) {
      throw new Error(`purpose tree contains a cycle at ${current}`);
    }
    guard.add(current);
    path.push(current);
    current = catalog.parents[current] ?? null;
  }
  return path.reverse();
}

export function knownPurpose(
  catalog: PurposeCatalog,
  node: PurposeNodeId,
): boolean {
  return Object.prototype.hasOwnProperty.call(catalog.parents, node);
}

/**
 * A grant at `grantedNode` covers `requestedNode` when the requested node is
 * the granted node itself or one of its descendants.
 */
export function purposeCovers(
  catalog: PurposeCatalog,
  grantedNode: PurposeNodeId,
  requestedNode: PurposeNodeId,
): boolean {
  let current: PurposeNodeId | null = requestedNode;
  while (current !== null) {
    if (current === grantedNode) {
      return true;
    }
    current = catalog.parents[current] ?? null;
  }
  return false;
}

export function knownRegion(catalog: RegionCatalog, region: RegionCode): boolean {
  return catalog.regions.has(region);
}

/** A granted region covers a request either exactly or via the wildcard. */
export function regionCovers(
  catalog: RegionCatalog,
  grantedRegion: RegionCode,
  requestedRegion: RegionCode,
): boolean {
  if (grantedRegion === requestedRegion) {
    return true;
  }
  return (
    catalog.wildcard !== undefined && grantedRegion === catalog.wildcard
  );
}

export function anyRegionCovers(
  catalog: RegionCatalog,
  grantedRegions: readonly RegionCode[],
  requestedRegion: RegionCode,
): boolean {
  return grantedRegions.some((granted) =>
    regionCovers(catalog, granted, requestedRegion),
  );
}
