/**
 * Compress a set of concrete resource paths into the shortest equivalent
 * list of patterns, using the resource catalog to decide when a "/*"
 * wildcard exactly covers a subtree.
 *
 * "a/b/*" is only emitted when every catalog path equal to or beneath
 * "a/b" is covered, so the compressed form never over-claims access.
 */
export function compressPaths(
  coveredPaths: string[],
  catalogPaths: string[],
): string[] {
  const covered = new Set(coveredPaths);
  const catalog = [...new Set(catalogPaths)];
  if (catalog.length === 0 || covered.size === 0) return [];

  const underOrEqual = (prefix: string): string[] =>
    catalog.filter((p) => p === prefix || p.startsWith(prefix + "/"));

  const childSegments = (prefix: string): string[] => {
    const segments = new Set<string>();
    for (const path of catalog) {
      if (prefix === "") segments.add(path.split("/")[0]);
      else if (path.startsWith(prefix + "/"))
        segments.add(path.slice(prefix.length + 1).split("/")[0]);
    }
    return [...segments];
  };

  const hasCoveredUnder = (prefix: string): boolean => {
    for (const path of covered) {
      if (path === prefix || path.startsWith(prefix + "/")) return true;
    }
    return false;
  };

  if (catalog.every((p) => covered.has(p))) return ["*"];

  const result: string[] = [];

  const emit = (prefix: string): void => {
    const subtree = underOrEqual(prefix);
    if (subtree.length === 0) {
      // Not a catalog prefix (defensive); keep the concrete path verbatim.
      if (covered.has(prefix)) result.push(prefix);
      return;
    }
    if (subtree.every((p) => covered.has(p))) {
      // A leaf resource stays an exact path; "x/*" is only used when it
      // actually spans more than the resource "x" itself.
      if (subtree.length === 1 && subtree[0] === prefix) result.push(prefix);
      else result.push(prefix + "/*");
      return;
    }
    if (covered.has(prefix)) result.push(prefix);
    for (const segment of childSegments(prefix)) {
      const child = prefix + "/" + segment;
      if (hasCoveredUnder(child)) emit(child);
    }
  };

  for (const segment of childSegments("")) {
    if (hasCoveredUnder(segment)) emit(segment);
  }

  return result.sort();
}
