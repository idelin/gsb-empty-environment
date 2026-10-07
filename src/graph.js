'use strict';

const path = require('path');

/**
 * Extract static import/export-from specifiers from a TypeScript source file
 * by walking the top-level AST statements.
 */
function parseImportSpecifiers(ts, fileName, sourceText) {
  const sourceFile = ts.createSourceFile(fileName, sourceText, ts.ScriptTarget.Latest, true);
  const specifiers = [];
  for (const statement of sourceFile.statements) {
    if (ts.isImportDeclaration(statement) || ts.isExportDeclaration(statement)) {
      const moduleSpecifier = statement.moduleSpecifier;
      if (moduleSpecifier && ts.isStringLiteral(moduleSpecifier)) {
        specifiers.push(moduleSpecifier.text);
      }
    } else if (ts.isImportEqualsDeclaration(statement)) {
      const ref = statement.moduleReference;
      if (ts.isExternalModuleReference(ref) && ts.isStringLiteral(ref.expression)) {
        specifiers.push(ref.expression.text);
      }
    }
  }
  return specifiers;
}

/**
 * Resolve a relative import specifier to a module id (posix path relative to
 * rootDir). Returns null for external (non-relative) specifiers, and undefined
 * when the specifier is relative but cannot be resolved to a known file.
 */
function resolveSpecifier(specifier, fromFile, fileSet, rootDir) {
  if (!specifier.startsWith('.')) return null; // external package, not tracked
  const base = path.resolve(path.dirname(fromFile), specifier);
  const candidates = [base, `${base}.ts`, `${base}.tsx`, `${base}.d.ts`, path.join(base, 'index.ts')];
  for (const candidate of candidates) {
    if (fileSet.has(candidate)) {
      return toModuleId(candidate, rootDir);
    }
  }
  return undefined;
}

function toModuleId(absPath, rootDir) {
  return path.relative(rootDir, absPath).split(path.sep).join('/');
}

/**
 * Tarjan's strongly connected components (iterative).
 * nodes: array of ids; adjacency: Map<id, Array<id>> (edges to internal deps).
 * Returns an array of SCCs, each an array of ids (sorted), with singletons
 * for acyclic nodes.
 */
function findStronglyConnectedComponents(nodes, adjacency) {
  const indexOf = new Map();
  const lowlink = new Map();
  const onStack = new Set();
  const stack = [];
  const sccs = [];
  let nextIndex = 0;

  for (const root of nodes) {
    if (indexOf.has(root)) continue;
    const work = [{ node: root, childIndex: 0 }];
    indexOf.set(root, nextIndex);
    lowlink.set(root, nextIndex);
    nextIndex += 1;
    stack.push(root);
    onStack.add(root);

    while (work.length > 0) {
      const frame = work[work.length - 1];
      const deps = adjacency.get(frame.node) || [];
      if (frame.childIndex < deps.length) {
        const dep = deps[frame.childIndex];
        frame.childIndex += 1;
        if (!indexOf.has(dep)) {
          indexOf.set(dep, nextIndex);
          lowlink.set(dep, nextIndex);
          nextIndex += 1;
          stack.push(dep);
          onStack.add(dep);
          work.push({ node: dep, childIndex: 0 });
        } else if (onStack.has(dep)) {
          lowlink.set(frame.node, Math.min(lowlink.get(frame.node), indexOf.get(dep)));
        }
      } else {
        work.pop();
        if (work.length > 0) {
          const parent = work[work.length - 1].node;
          lowlink.set(parent, Math.min(lowlink.get(parent), lowlink.get(frame.node)));
        }
        if (lowlink.get(frame.node) === indexOf.get(frame.node)) {
          const scc = [];
          let member;
          do {
            member = stack.pop();
            onStack.delete(member);
            scc.push(member);
          } while (member !== frame.node);
          scc.sort();
          sccs.push(scc);
        }
      }
    }
  }
  return sccs;
}

/**
 * Order SCCs so that every group appears after the groups it depends on
 * (dependencies first). Edges between groups never form cycles by construction.
 */
function orderGroupsByDependency(sccs, adjacency) {
  const groupOf = new Map();
  sccs.forEach((scc, i) => scc.forEach((id) => groupOf.set(id, i)));
  const indegree = sccs.map(() => 0);
  const dependents = sccs.map(() => []);
  sccs.forEach((scc, i) => {
    const seen = new Set();
    for (const id of scc) {
      for (const dep of adjacency.get(id) || []) {
        const depGroup = groupOf.get(dep);
        if (depGroup !== i && !seen.has(depGroup)) {
          seen.add(depGroup);
          indegree[i] += 1;
          dependents[depGroup].push(i);
        }
      }
    }
  });
  const ready = [];
  indegree.forEach((deg, i) => {
    if (deg === 0) ready.push(i);
  });
  const order = [];
  while (ready.length > 0) {
    const i = ready.shift();
    order.push(sccs[i]);
    for (const next of dependents[i]) {
      indegree[next] -= 1;
      if (indegree[next] === 0) ready.push(next);
    }
  }
  if (order.length !== sccs.length) {
    throw new Error('Condensation graph unexpectedly contains a cycle');
  }
  return order;
}

module.exports = {
  parseImportSpecifiers,
  resolveSpecifier,
  toModuleId,
  findStronglyConnectedComponents,
  orderGroupsByDependency,
};
