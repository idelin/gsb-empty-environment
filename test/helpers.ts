import { builtinSpaces, ConversionGraph, type CompiledGraph } from '../src/index.ts';

export function standardGraph(): ConversionGraph {
  const graph = new ConversionGraph();
  for (const space of builtinSpaces()) {
    graph.addSpace(space);
  }
  return graph;
}

export function standardCompiledGraph(): CompiledGraph {
  return standardGraph().compile();
}

export function approxVec3(
  actual: readonly [number, number, number],
  expected: readonly [number, number, number],
  tolerance: number,
): boolean {
  return (
    Math.abs(actual[0] - expected[0]) <= tolerance &&
    Math.abs(actual[1] - expected[1]) <= tolerance &&
    Math.abs(actual[2] - expected[2]) <= tolerance
  );
}
