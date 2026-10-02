/**
 * Patch operations: the minimal, precisely-targeted mutations the engine
 * emits when a signal changes. `target` is a stable path assigned at mount
 * time (e.g. "0/2/0" = root -> child 2 -> child 0), so a patch can be
 * applied directly without walking or diffing the tree.
 */
import type { VAnchor, VElement, VNode } from "./vnode.ts";

export type Patch =
  | { op: "UpdateTextNode"; target: string; value: string }
  | { op: "SetAttribute"; target: string; name: string; value: string }
  | { op: "RemoveAttribute"; target: string; name: string }
  | { op: "InsertNode"; target: string; node: VNode }
  | { op: "RemoveNode"; target: string };

export function findNode(root: VNode, path: string): VNode {
  const segments = path.split("/").map(Number);
  if (segments[0] !== 0) throw new Error(`Invalid path "${path}": must start at root`);
  let node = root;
  for (let index = 1; index < segments.length; index++) {
    if (node.kind !== "element") {
      throw new Error(`Invalid path "${path}": segment ${index} crosses a non-element node`);
    }
    const child = node.children[segments[index]];
    if (child === undefined) throw new Error(`Invalid path "${path}": no child at segment ${index}`);
    node = child;
  }
  return node;
}

export function applyPatch(root: VElement, patch: Patch): void {
  const node = findNode(root, patch.target);
  switch (patch.op) {
    case "UpdateTextNode": {
      if (node.kind !== "text") throw new Error(`UpdateTextNode targets ${node.kind} at ${patch.target}`);
      node.value = patch.value;
      return;
    }
    case "SetAttribute": {
      if (node.kind !== "element") throw new Error(`SetAttribute targets ${node.kind} at ${patch.target}`);
      node.attrs[patch.name] = patch.value;
      return;
    }
    case "RemoveAttribute": {
      if (node.kind !== "element") throw new Error(`RemoveAttribute targets ${node.kind} at ${patch.target}`);
      delete node.attrs[patch.name];
      return;
    }
    case "InsertNode": {
      if (node.kind !== "anchor") throw new Error(`InsertNode targets ${node.kind} at ${patch.target}`);
      (node as VAnchor).current = patch.node;
      return;
    }
    case "RemoveNode": {
      if (node.kind !== "anchor") throw new Error(`RemoveNode targets ${node.kind} at ${patch.target}`);
      (node as VAnchor).current = null;
      return;
    }
  }
}

export function applyPatches(root: VElement, patches: readonly Patch[]): void {
  for (const patch of patches) applyPatch(root, patch);
}

const VOID_TAGS = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "source", "track", "wbr"]);

function escapeText(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttr(value: string): string {
  return escapeText(value).replace(/"/g, "&quot;");
}

export function toHTML(node: VNode | null): string {
  if (node === null) return "";
  switch (node.kind) {
    case "text":
      return escapeText(node.value);
    case "anchor":
      return toHTML(node.current);
    case "element": {
      const attrs = Object.entries(node.attrs)
        .map(([name, value]) => ` ${name}="${escapeAttr(value)}"`)
        .join("");
      if (VOID_TAGS.has(node.tag)) return `<${node.tag}${attrs}>`;
      const inner = node.children.map(toHTML).join("");
      return `<${node.tag}${attrs}>${inner}</${node.tag}>`;
    }
  }
}
