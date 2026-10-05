// AST editing helpers.
//
// replaceNode keeps the replaced node's *external* comments: the leading
// and trailing comments of the target are transferred onto the replacement,
// so printing the edited tree never drops them. Unchanged subtrees keep
// their own comments untouched and in order.

import { children, parse } from './parser.js';

// Depth-first search for the first node matching pred.
export function find(node, pred) {
  if (pred(node)) return node;
  for (const child of children(node)) {
    const hit = find(child, pred);
    if (hit) return hit;
  }
  return null;
}

// Parse a standalone expression to use as a replacement subtree.
export function parseExpr(src) {
  return parse(src).root;
}

// Replace `target` inside `root` with `replacement` (mutates and returns
// the tree; if root itself is replaced, returns the new root).
export function replaceNode(root, target, replacement) {
  const adopt = (node) => {
    replacement.leading = node.leading || [];
    replacement.trailing = node.trailing || null;
    return replacement;
  };
  if (root === target) return adopt(root);
  const visit = (node) => {
    switch (node.kind) {
      case 'unary':
        if (node.arg === target) node.arg = adopt(node.arg);
        else visit(node.arg);
        break;
      case 'binary':
        if (node.left === target) node.left = adopt(node.left);
        else visit(node.left);
        if (node.right === target) node.right = adopt(node.right);
        else visit(node.right);
        break;
      case 'call':
        for (let i = 0; i < node.args.length; i++) {
          if (node.args[i] === target) node.args[i] = adopt(node.args[i]);
          else visit(node.args[i]);
        }
        break;
      case 'paren':
        if (node.expr === target) node.expr = adopt(node.expr);
        else visit(node.expr);
        break;
      default:
        break;
    }
  };
  visit(root);
  return root;
}
