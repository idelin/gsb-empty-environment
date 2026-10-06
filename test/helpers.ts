import assert from "node:assert/strict";
import type { CanvasNode, NodeId } from "../src/model.ts";

/**
 * Assert that `pasted` is isomorphic to `source` under `idMap`:
 * a bijection on node sets that preserves node types and the internal
 * reference structure (a -> b in source  <=>  f(a) -> f(b) in pasted).
 */
export function assertIsomorphic(
  source: readonly CanvasNode[],
  pasted: readonly CanvasNode[],
  idMap: ReadonlyMap<NodeId, NodeId>,
): void {
  assert.equal(pasted.length, source.length, "node count differs");

  const sourceIds = new Set(source.map((n) => n.id));
  const pastedById = new Map(pasted.map((n) => [n.id, n]));

  // idMap is a bijection source ids -> pasted ids.
  assert.equal(idMap.size, source.length, "idMap is not total on the source set");
  assert.equal(new Set(idMap.values()).size, source.length, "idMap is not injective");
  for (const newId of idMap.values()) {
    assert.ok(pastedById.has(newId), `idMap points at unknown pasted node ${newId}`);
    assert.ok(!sourceIds.has(newId), `pasted id ${newId} collides with a source id`);
  }

  for (const s of source) {
    const p = pastedById.get(idMap.get(s.id)!)!;
    assert.equal(p.type, s.type, `type mismatch for ${s.id}`);
    assert.deepEqual(p.props, s.props, `props mismatch for ${s.id}`);

    const sourceInternal = s.refs.filter((ref) => ref.target !== null && sourceIds.has(ref.target));
    const pastedInternal = p.refs.filter((ref) => ref.target !== null && pastedById.has(ref.target));
    assert.equal(
      pastedInternal.length,
      sourceInternal.length,
      `internal ref count mismatch for ${s.id}`,
    );
    for (const ref of sourceInternal) {
      const mapped = p.refs.find((candidate) => candidate.key === ref.key);
      assert.ok(mapped, `missing ref slot ${ref.key} on pasted ${p.id}`);
      assert.equal(mapped.status, "ok", `internal ref ${s.id}.${ref.key} is broken`);
      assert.equal(
        mapped.target,
        idMap.get(ref.target!),
        `internal ref ${s.id}.${ref.key} not remapped to the new id`,
      );
      assert.equal(mapped.expectedType, ref.expectedType);
    }
  }
}

export function node(
  id: NodeId,
  type: string,
  refs: Array<{ key: string; expectedType: string; target: NodeId | null }> = [],
  props: Record<string, unknown> = {},
): CanvasNode {
  return {
    id,
    type,
    props,
    refs: refs.map((ref) => ({ ...ref, status: ref.target === null ? "broken" : "ok" })),
  };
}
