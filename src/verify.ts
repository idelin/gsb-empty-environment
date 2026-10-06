import { childrenOf, type Node, type Program } from "./ast.ts";
import type { BindResult } from "./binder.ts";

/**
 * Assigns every node a stable tree path ("" for the root, then child-index
 * segments). Two parses of structurally identical programs produce identical
 * paths for corresponding nodes, independent of source offsets.
 */
export function computePaths(root: Program): Map<Node, string> {
  const paths = new Map<Node, string>();
  const walk = (node: Node, path: string): void => {
    paths.set(node, path);
    childrenOf(node).forEach((child, i) => walk(child, `${path}/${i}`));
  };
  walk(root, "");
  return paths;
}

/**
 * Binding-graph isomorphism check between an original program and a renamed
 * re-parse. Both trees must have identical shape; identifier/declaration
 * spelling may differ only at `editedPaths` (where the text must equal
 * `newName`); every reference must resolve to the binding at the same tree
 * path in both graphs (unbound must stay unbound). Returns null when the
 * graphs are isomorphic, otherwise a human-readable description of the
 * first differences found.
 */
export function verifyIsomorphism(
  oldProgram: Program,
  oldBind: BindResult,
  newProgram: Program,
  newBind: BindResult,
  editedPaths: ReadonlySet<string>,
  newName: string,
): string | null {
  const oldPaths = computePaths(oldProgram);
  const newPaths = computePaths(newProgram);
  const errors: string[] = [];

  const walk = (o: Node, n: Node, path: string): void => {
    if (errors.length >= 10) return;
    if (o.kind !== n.kind) {
      errors.push(`node kind mismatch at ${path}: ${o.kind} vs ${n.kind}`);
      return;
    }
    if (o.kind === "Ident" && n.kind === "Ident") {
      if (editedPaths.has(path)) {
        if (n.text !== newName) {
          errors.push(`reference at ${path} should have been renamed to '${newName}' but is '${n.text}'`);
        }
      } else if (o.text !== n.text) {
        errors.push(`non-target reference at ${path} changed from '${o.text}' to '${n.text}'`);
      }
      const ro = oldBind.resolutions.get(o) ?? null;
      const rn = newBind.resolutions.get(n) ?? null;
      const po = ro ? oldPaths.get(ro.decl) ?? "<unknown>" : null;
      const pn = rn ? newPaths.get(rn.decl) ?? "<unknown>" : null;
      if (po !== pn) {
        errors.push(`resolution of '${o.text}' at ${path} changed: binding ${po ?? "<unbound>"} -> ${pn ?? "<unbound>"}`);
      }
    }
    if (o.kind === "LetDecl" && n.kind === "LetDecl") {
      if (editedPaths.has(path)) {
        if (n.name !== newName) {
          errors.push(`declaration at ${path} should have been renamed to '${newName}' but is '${n.name}'`);
        }
      } else if (o.name !== n.name) {
        errors.push(`non-target declaration at ${path} changed from '${o.name}' to '${n.name}'`);
      }
    }
    const oc = childrenOf(o);
    const nc = childrenOf(n);
    if (oc.length !== nc.length) {
      errors.push(`child count mismatch at ${path}: ${oc.length} vs ${nc.length}`);
      return;
    }
    oc.forEach((child, i) => walk(child, nc[i], `${path}/${i}`));
  };

  walk(oldProgram, newProgram, "");
  if (oldBind.bindings.length !== newBind.bindings.length) {
    errors.push(`binding count changed: ${oldBind.bindings.length} -> ${newBind.bindings.length}`);
  }
  return errors.length > 0 ? errors.join("; ") : null;
}
