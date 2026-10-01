/**
 * Expression DAG over metrics.
 *
 * Nodes: source (raw records) -> sum (multi-level aggregation) ->
 * ratio -> pp-change / rel-change. Every evaluation carries provenance:
 *  - contributions: the exact BigInt amounts each leaf record added to
 *    the numerator/denominator sums (0n when excluded), and
 *  - missing events: every missing/retracted field, propagated upward
 *    so consumers can see exactly which record poisoned a result.
 */

import {
  aggregate,
  mergeAggregates,
  ratioOf,
  type Aggregate,
  type Observation,
  type RatioValue,
} from './measure.ts';
import {
  ppChange,
  relChange,
  type ChangeValue,
} from './changes.ts';

export interface Contribution {
  readonly observationId: string;
  /** exact contribution to the numerator sum (0n when not counted) */
  readonly numerator: bigint;
  /** exact contribution to the denominator sum (0n when not counted) */
  readonly denominator: bigint;
  readonly status: 'counted' | 'unpaired' | 'excluded';
}

export interface MissingEvent {
  readonly observationId: string;
  readonly field: 'numerator' | 'denominator';
  readonly status: 'missing' | 'retracted';
}

export interface Provenance {
  readonly contributions: readonly Contribution[];
  readonly missing: readonly MissingEvent[];
}

export type GraphNode =
  | { readonly type: 'source'; readonly name: string; readonly observations: readonly Observation[] }
  | { readonly type: 'sum'; readonly name: string; readonly inputs: readonly GraphNode[] }
  | { readonly type: 'ratio'; readonly name: string; readonly input: GraphNode }
  | { readonly type: 'pp-change'; readonly name: string; readonly next: GraphNode; readonly prev: GraphNode }
  | { readonly type: 'rel-change'; readonly name: string; readonly next: GraphNode; readonly prev: GraphNode };

export type GraphValue =
  | { readonly kind: 'aggregate'; readonly aggregate: Aggregate; readonly prov: Provenance }
  | { readonly kind: 'ratio'; readonly ratio: RatioValue; readonly prov: Provenance }
  | { readonly kind: 'change'; readonly change: ChangeValue; readonly prov: Provenance };

export function source(name: string, observations: readonly Observation[]): GraphNode {
  return { type: 'source', name, observations };
}

export function sum(name: string, inputs: readonly GraphNode[]): GraphNode {
  return { type: 'sum', name, inputs };
}

export function ratio(name: string, input: GraphNode): GraphNode {
  return { type: 'ratio', name, input };
}

export function ppChangeNode(name: string, next: GraphNode, prev: GraphNode): GraphNode {
  return { type: 'pp-change', name, next, prev };
}

export function relChangeNode(name: string, next: GraphNode, prev: GraphNode): GraphNode {
  return { type: 'rel-change', name, next, prev };
}

function leafProvenance(o: Observation): Provenance {
  const missing: MissingEvent[] = [];
  if (o.numerator.status !== 'present') {
    missing.push({ observationId: o.id, field: 'numerator', status: o.numerator.status });
  }
  if (o.denominator.status !== 'present') {
    missing.push({ observationId: o.id, field: 'denominator', status: o.denominator.status });
  }
  const counted = o.numerator.status === 'present' && o.denominator.status === 'present';
  const anyPresent = o.numerator.status === 'present' || o.denominator.status === 'present';
  const contribution: Contribution = {
    observationId: o.id,
    numerator: counted && o.numerator.status === 'present' ? o.numerator.value : 0n,
    denominator: counted && o.denominator.status === 'present' ? o.denominator.value : 0n,
    status: counted ? 'counted' : anyPresent ? 'unpaired' : 'excluded',
  };
  return { contributions: [contribution], missing };
}

function concatProv(parts: readonly Provenance[]): Provenance {
  return {
    contributions: parts.flatMap((p) => p.contributions),
    missing: parts.flatMap((p) => p.missing),
  };
}

function expectKind<K extends GraphValue['kind']>(
  value: GraphValue,
  kind: K,
  nodeName: string,
): Extract<GraphValue, { kind: K }> {
  if (value.kind !== kind) {
    throw new TypeError(`node '${nodeName}' expects input of kind '${kind}', got '${value.kind}'`);
  }
  return value as Extract<GraphValue, { kind: K }>;
}

export function evaluate(node: GraphNode): GraphValue {
  switch (node.type) {
    case 'source': {
      return {
        kind: 'aggregate',
        aggregate: aggregate(node.observations),
        prov: concatProv(node.observations.map(leafProvenance)),
      };
    }
    case 'sum': {
      const inputs = node.inputs.map((i) => expectKind(evaluate(i), 'aggregate', node.name));
      return {
        kind: 'aggregate',
        aggregate: mergeAggregates(inputs.map((i) => i.aggregate)),
        prov: concatProv(inputs.map((i) => i.prov)),
      };
    }
    case 'ratio': {
      const input = expectKind(evaluate(node.input), 'aggregate', node.name);
      return { kind: 'ratio', ratio: ratioOf(input.aggregate), prov: input.prov };
    }
    case 'pp-change': {
      const next = expectKind(evaluate(node.next), 'ratio', node.name);
      const prev = expectKind(evaluate(node.prev), 'ratio', node.name);
      return {
        kind: 'change',
        change: ppChange(next.ratio, prev.ratio),
        prov: concatProv([next.prov, prev.prov]),
      };
    }
    case 'rel-change': {
      const next = expectKind(evaluate(node.next), 'ratio', node.name);
      const prev = expectKind(evaluate(node.prev), 'ratio', node.name);
      return {
        kind: 'change',
        change: relChange(next.ratio, prev.ratio),
        prov: concatProv([next.prov, prev.prov]),
      };
    }
  }
}
