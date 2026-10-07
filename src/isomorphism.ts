import type { CanvasNode, NodeId } from './graph.ts';

/**
 * Check that a pasted subgraph is isomorphic to the copied source under
 * the paste id mapping: same node count, same types, and the same
 * internal wiring once ids are translated. External refs must be either
 * preserved verbatim (same target id) or explicitly marked dangling.
 */
export function isIsomorphicPaste(
  source: readonly CanvasNode[],
  pasted: readonly CanvasNode[],
  idMap: ReadonlyMap<NodeId, NodeId>,
): boolean {
  if (source.length !== pasted.length) return false;

  const sourceIds = new Set(source.map((n) => n.id));
  const pastedById = new Map(pasted.map((n) => [n.id, n]));

  // idMap must be a bijection source ids -> pasted ids.
  if (idMap.size !== source.length) return false;
  const image = new Set(idMap.values());
  if (image.size !== idMap.size) return false;
  for (const id of image) {
    if (!pastedById.has(id)) return false;
  }

  for (const src of source) {
    const copy = pastedById.get(idMap.get(src.id)!);
    if (copy === undefined) return false;
    if (copy.type !== src.type) return false;
    if (copy.refs.length !== src.refs.length) return false;

    for (let i = 0; i < src.refs.length; i += 1) {
      const sRef = src.refs[i]!;
      const cRef = copy.refs[i]!;
      if (sRef.key !== cRef.key) return false;
      if (sRef.kind === 'dangling') {
        if (cRef.kind !== 'dangling') return false;
        continue;
      }
      if (sourceIds.has(sRef.targetId)) {
        // Internal edge: must be rewired through the same bijection.
        if (cRef.kind !== 'ref') return false;
        if (cRef.targetId !== idMap.get(sRef.targetId)) return false;
        if (cRef.targetType !== sRef.targetType) return false;
      } else {
        // External edge: kept verbatim or explicitly broken.
        const kept =
          cRef.kind === 'ref' &&
          cRef.targetId === sRef.targetId &&
          cRef.targetType === sRef.targetType;
        const broken =
          cRef.kind === 'dangling' &&
          cRef.originalTargetId === sRef.targetId;
        if (!kept && !broken) return false;
      }
    }
  }
  return true;
}
