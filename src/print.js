'use strict';

const { parse } = require('./parser');

const PREC = { '+': 1, '-': 1, '*': 2, '/': 2 };

// Canonically render an AST node (synthetic or parsed) back to text such
// that parsing the text yields a structurally equal AST. Parentheses are
// added only where the grammar would otherwise change the shape.
function render(node) {
  switch (node.kind) {
    case 'num':
      return String(node.value);
    case 'ident':
      return node.name;
    case 'call':
      return `${node.name}(${node.args.map(render).join(', ')})`;
    case 'neg': {
      const inner = node.expr.kind === 'bin'
        ? `(${render(node.expr)})`
        : render(node.expr);
      return `-${inner}`;
    }
    case 'bin': {
      const left = renderBinChild(node.left, node, true);
      const right = renderBinChild(node.right, node, false);
      return `${left} ${node.op} ${right}`;
    }
    default:
      throw new Error(`cannot render node kind ${node.kind}`);
  }
}

function renderBinChild(child, parent, isLeft) {
  if (child.kind !== 'bin') return render(child);
  const needParens =
    PREC[child.op] < PREC[parent.op] ||
    // Operators are left-associative, so an equal-precedence right child
    // must keep its parentheses to preserve the AST shape.
    (!isLeft && PREC[child.op] === PREC[parent.op]);
  return needParens ? `(${render(child)})` : render(child);
}

// Reprint a whole token stream with normalized whitespace: tokens are
// separated by single spaces (so no two tokens can merge), leading
// comments stay on their own lines before their token, trailing comments
// follow their token on the same line, and tail comments (leading trivia
// of the EOF token) are preserved at the end of the file.
function printTokens(tokens) {
  let out = '';
  let needSpace = false;
  for (const tok of tokens) {
    for (const comment of tok.leading) {
      if (out.length > 0 && !out.endsWith('\n')) out += '\n';
      out += comment.text + '\n';
      needSpace = false;
    }
    if (tok.type === 'eof') break;
    if (needSpace) out += ' ';
    out += tok.text;
    needSpace = true;
    if (tok.trailing.length > 0) {
      out += ' ' + tok.trailing.map((c) => c.text).join(' ') + '\n';
      needSpace = false;
    }
  }
  return out;
}

// Reprint a parse result without changing its AST or losing any comment.
function print(parsed) {
  return printTokens(parsed.tokens);
}

// Normalize whitespace of a source string; idempotent.
function format(src) {
  return print(parse(src));
}

// Replace the source span of `target` (a node of parsed.ast) with the
// canonical rendering of `replacement` (any AST node). Comments attached
// outside the node — leading trivia of its first token and trailing
// trivia of its last token — live outside the spliced span and are kept.
function replaceNode(parsed, target, replacement) {
  const first = parsed.tokens[target.tok0];
  const last = parsed.tokens[target.tok1];
  return parsed.src.slice(0, first.start) + render(replacement) + parsed.src.slice(last.end);
}

module.exports = { render, print, printTokens, format, replaceNode };
