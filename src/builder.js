'use strict';

const { hash } = require('./hash');
const { compile, signatureHash } = require('./compiler');
const { buildGraph, tarjanSCC, topoSortSCCs } = require('./graph');
const { BuildCache } = require('./cache');

/**
 * Incremental builder.
 *
 * Cache key for a strongly connected component S:
 *   H( sorted [H(id, source) for id in S]
 *      + sorted [depId : exportSignatureHash(dep) for direct deps outside S] )
 *
 * Consequences:
 * - A dependency's internal implementation change does not alter its
 *   export signature, so dependents' keys are unchanged and they are
 *   served from cache.
 * - A dependency's export change alters its signature hash, which
 *   changes dependents' keys, transitively down the graph.
 * - Cyclic modules form one SCC and are compiled (and cached) as a unit.
 * - Artifacts are committed to the cache only after every member of the
 *   SCC compiles successfully, so failures never enter the cache.
 */
class Builder {
  constructor({ cache } = {}) {
    this.cache = cache || new BuildCache();
  }

  /**
   * @param {Object<string,string>|Map<string,string>} input module id -> TypeScript source
   * @returns {{ outputs: Map<string,string>, stats: { compiled: string[], reused: string[], compileCount: number } }}
   */
  build(input) {
    const sources = input instanceof Map ? input : new Map(Object.entries(input));
    const graph = buildGraph(sources);
    const orderedSCCs = topoSortSCCs(graph, tarjanSCC(graph));

    const outputs = new Map();
    const signatures = new Map(); // id -> signatureHash
    const compiled = [];
    const reused = [];

    for (const scc of orderedSCCs) {
      const sorted = scc.slice().sort();
      const scopeKey = sorted.join('|');
      const inSCC = new Set(scc);

      const sourcePart = sorted.map((id) => hash(id + '\0' + sources.get(id))).join('|');
      const depPart = [];
      for (const id of scc) {
        for (const dep of graph.get(id)) {
          if (!inSCC.has(dep)) depPart.push(`${dep}:${signatures.get(dep)}`);
        }
      }
      depPart.sort();
      const key = hash(sourcePart + '#' + depPart.join('|'));

      const cached = this.cache.get(scopeKey);
      if (cached && cached.key === key) {
        for (const id of scc) {
          const member = cached.members.get(id);
          outputs.set(id, member.output);
          signatures.set(id, member.signatureHash);
          reused.push(id);
        }
        continue;
      }

      // Cache miss: compile the whole SCC; commit only if all succeed.
      const members = new Map();
      for (const id of sorted) {
        const { js, signature } = compile(id, sources.get(id)); // throws CompileError
        members.set(id, { output: js, signature, signatureHash: signatureHash(signature) });
      }
      this.cache.set(scopeKey, { key, members });
      for (const id of scc) {
        outputs.set(id, members.get(id).output);
        signatures.set(id, members.get(id).signatureHash);
        compiled.push(id);
      }
    }

    return { outputs, stats: { compiled, reused, compileCount: compiled.length } };
  }
}

module.exports = { Builder };
