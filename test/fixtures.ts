import type {
  Grammar,
  GrammarProduction,
  PolicyTable,
  TypeGraph,
  TypeNode,
  TypeRef,
  Variance,
} from '../src/types.ts';
import { RULE_VERSION } from '../src/inclusion.ts';

export const KEY_A = 'pk-aaa';
export const KEY_B = 'pk-bbb';

export const asm = (version: string, publicKey = KEY_A, name = 'CoreLib') => ({
  version,
  publicKey,
  name,
});

export const asmRef = (version: string, publicKey = KEY_A, name = 'CoreLib') => ({
  name,
  publicKey,
  versions: [{ min: version, max: version }],
});

interface NodeSpec {
  id: string;
  typeName: string;
  version?: string;
  publicKey?: string;
  name?: string;
  args?: TypeRef[];
  arrayRank?: number;
  element?: TypeRef;
}

export function node(spec: NodeSpec): TypeNode {
  return {
    id: spec.id,
    assembly: asm(spec.version ?? '1.0.0.0', spec.publicKey ?? KEY_A, spec.name ?? 'CoreLib'),
    typeName: spec.typeName,
    args: spec.args,
    arrayRank: spec.arrayRank,
    element: spec.element,
  };
}

export function graph(root: TypeRef, nodes: TypeNode[]): TypeGraph {
  const map: Record<string, TypeNode> = {};
  for (const item of nodes) map[item.id] = item;
  return { root, nodes: map };
}

const arg = (nt: string, variance: Variance = 'invariant') => ({ nt, variance });

/**
 * Baseline language:
 *   S    -> string | List<S>            (List is covariant)
 *   P    -> Pair<P, P> | string         (both positions invariant)
 *   ARR  -> string[] | List<ARR>
 */
export function baselineGrammar(): Grammar {
  const productions: GrammarProduction[] = [
    {
      nt: 'S',
      alts: [
        { assembly: asmRef('1.0.0.0'), typeName: 'String' },
        {
          assembly: asmRef('1.0.0.0'),
          typeName: 'List`1',
          args: [arg('S', 'covariant')],
        },
      ],
    },
    {
      nt: 'P',
      alts: [
        { assembly: asmRef('1.0.0.0'), typeName: 'String' },
        {
          assembly: asmRef('1.0.0.0'),
          typeName: 'Pair`2',
          args: [arg('P', 'invariant'), arg('P', 'invariant')],
        },
      ],
    },
    {
      nt: 'ARR',
      alts: [
        {
          assembly: asmRef('1.0.0.0'),
          typeName: 'Array',
          arrayRank: 1,
          element: 'S',
        },
        {
          assembly: asmRef('1.0.0.0'),
          typeName: 'List`1',
          args: [arg('ARR', 'covariant')],
        },
      ],
    },
  ];
  return { ruleVersion: RULE_VERSION, start: 'S', productions };
}

export function baselinePolicy(): PolicyTable {
  const grammar = baselineGrammar();
  return {
    ruleVersion: RULE_VERSION,
    start: grammar.start,
    productions: grammar.productions,
    renames: [],
  };
}

/** Recursive List<String> graph: l1 -> List(l2 -> List(... l1)) with a
 *  guarded back-reference (every cycle crosses the List constructor). */
export function recursiveListGraph(depth = 3): TypeGraph {
  const nodes: TypeNode[] = [];
  for (let i = 1; i <= depth; i++) {
    const child = i === depth ? 'l1' : `l${i + 1}`;
    nodes.push(node({ id: `l${i}`, typeName: 'List`1', args: [child] }));
  }
  nodes.push(node({ id: 's1', typeName: 'String' }));
  // Rewire the innermost list to close on a guarded chain ending at string.
  nodes[depth - 1].args = ['s1'];
  return graph('l1', nodes);
}

/** Truly cyclic graph: c1 = List<c1> (guarded by the List constructor). */
export function cyclicListGraph(): TypeGraph {
  const c1 = node({ id: 'c1', typeName: 'List`1', args: ['c1'] });
  return graph('c1', [c1]);
}

/** Bare alias ring: a1 -> a2 -> a1 with no constructor in between. */
export function bareAliasGraph(): TypeGraph {
  const a1 = node({ id: 'a1', typeName: 'Alias', args: ['a2'] });
  const a2 = node({ id: 'a2', typeName: 'Alias', args: ['a1'] });
  return graph('a1', [a1, a2]);
}

/** Two shared-node positions: one expects S (covariant), one expects P. */
export function sharedPositionGraph(): { graph: TypeGraph; grammar: Grammar } {
  const grammarObj = baselineGrammar();
  grammarObj.productions.push({
    nt: 'Mixed',
    alts: [
      {
        assembly: asmRef('1.0.0.0'),
        typeName: 'Holder`2',
        args: [arg('S', 'covariant'), arg('P', 'invariant')],
      },
    ],
  });
  grammarObj.start = 'Mixed';
  const shared = node({ id: 'shared', typeName: 'String' });
  const holder = node({ id: 'h', typeName: 'Holder`2', args: ['shared', 'shared'] });
  return { graph: graph('h', [holder, shared]), grammar: grammarObj };
}

/** Deep linear nesting beyond the default JS call stack budget. */
export function deepListGraph(depth: number): TypeGraph {
  const nodes: TypeNode[] = [];
  for (let i = 1; i <= depth; i++) {
    nodes.push(node({ id: `d${i}`, typeName: 'List`1', args: [i === depth ? 'leaf' : `d${i + 1}`] }));
  }
  nodes.push(node({ id: 'leaf', typeName: 'String' }));
  return graph('d1', nodes);
}

/** Same type name emitted by two assemblies with different public keys. */
export function sameNameDifferentKeyGraph(): TypeGraph {
  const evil = node({
    id: 'evil',
    typeName: 'String',
    publicKey: KEY_B,
  });
  return graph('evil', [evil]);
}
