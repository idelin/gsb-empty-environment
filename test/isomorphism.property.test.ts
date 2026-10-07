import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Graph, type CanvasNode, type NodeRef } from '../src/graph.ts';
import { IdAllocator } from '../src/id-allocator.ts';
import { pasteSubgraph } from '../src/paste.ts';
import { isIsomorphicPaste } from '../src/isomorphism.ts';

/** Deterministic PRNG (mulberry32) so failures are reproducible. */
function rng(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const TYPES = ['box', 'port', 'label'] as const;

function randomGraph(random: () => number): {
  graph: Graph;
  clipboard: CanvasNode[];
} {
  const graph = new Graph();
  const externalCount = Math.floor(random() * 6);
  const externalIds: string[] = [];
  for (let i = 0; i < externalCount; i += 1) {
    const id = `ext_${i}`;
    graph.add({
      id,
      type: TYPES[Math.floor(random() * TYPES.length)]!,
      refs: [],
    });
    externalIds.push(id);
  }

  const copiedCount = 1 + Math.floor(random() * 8);
  const clipboard: CanvasNode[] = [];
  const copiedIds: string[] = [];
  const copiedTypes: string[] = [];
  for (let i = 0; i < copiedCount; i += 1) {
    copiedIds.push(`c_${i}`);
    copiedTypes.push(TYPES[Math.floor(random() * TYPES.length)]!);
  }
  for (let i = 0; i < copiedCount; i += 1) {
    const refs: NodeRef[] = [];
    const refCount = Math.floor(random() * 4);
    for (let slot = 0; slot < refCount; slot += 1) {
      const roll = random();
      const type = TYPES[Math.floor(random() * TYPES.length)]!;
      if (roll < 0.5 && copiedIds.length > 0) {
        // internal edge: declared type matches the actual source node type
        const targetIndex = Math.floor(random() * copiedIds.length);
        refs.push({
          kind: 'ref',
          key: `slot_${slot}`,
          targetId: copiedIds[targetIndex]!,
          targetType: copiedTypes[targetIndex]!,
        });
      } else if (roll < 0.8 && externalIds.length > 0) {
        // external edge: sometimes the declared type matches, sometimes not
        const targetId =
          externalIds[Math.floor(random() * externalIds.length)]!;
        const actualType = graph.get(targetId)!.type;
        refs.push({
          kind: 'ref',
          key: `slot_${slot}`,
          targetId,
          targetType: random() < 0.5 ? actualType : type,
        });
      } else {
        // edge to a node that does not exist anywhere
        refs.push({
          kind: 'ref',
          key: `slot_${slot}`,
          targetId: `ghost_${slot}`,
          targetType: type,
        });
      }
    }
    clipboard.push({ id: copiedIds[i]!, type: copiedTypes[i]!, refs });
  }
  return { graph, clipboard };
}

test('property: every random paste is isomorphic and advances ids exactly', () => {
  for (let seed = 1; seed <= 200; seed += 1) {
    const { graph, clipboard } = randomGraph(rng(seed));
    const allocator = new IdAllocator();
    const before = allocator.cursor;

    const result = pasteSubgraph(clipboard, graph, allocator);

    assert.ok(
      isIsomorphicPaste(clipboard, result.nodes, result.idMap),
      `seed ${seed}: pasted subgraph must be isomorphic to the source`,
    );
    assert.equal(
      allocator.cursor,
      before + clipboard.length,
      `seed ${seed}: id sequence must advance by exactly the pasted count`,
    );
    // Every kept external ref must point at a live node of matching type.
    for (const pasted of result.nodes) {
      for (const ref of pasted.refs) {
        if (ref.kind !== 'ref') continue;
        const target = graph.get(ref.targetId);
        assert.ok(target, `seed ${seed}: live ref target must exist`);
        assert.equal(
          target!.type,
          ref.targetType,
          `seed ${seed}: live ref target type must match`,
        );
      }
    }
  }
});
