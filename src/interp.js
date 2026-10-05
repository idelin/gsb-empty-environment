'use strict';

// Independent integer interpreter used to cross-check semantics.
// env = { vars: {name: int}, funcs: {name: (...ints) => int} }
// Integer division truncates toward zero; division by zero throws.
function evaluate(node, env = { vars: {}, funcs: {} }) {
  switch (node.kind) {
    case 'num':
      return node.value;
    case 'ident': {
      if (!(node.name in env.vars)) throw new Error(`unbound variable: ${node.name}`);
      return env.vars[node.name];
    }
    case 'neg':
      return -evaluate(node.expr, env);
    case 'call': {
      const fn = env.funcs[node.name];
      if (!fn) throw new Error(`unknown function: ${node.name}`);
      return fn(...node.args.map((arg) => evaluate(arg, env)));
    }
    case 'bin': {
      const l = evaluate(node.left, env);
      const r = evaluate(node.right, env);
      switch (node.op) {
        case '+': return l + r;
        case '-': return l - r;
        case '*': return l * r;
        case '/':
          if (r === 0) throw new Error('division by zero');
          return Math.trunc(l / r);
        default:
          throw new Error(`unknown operator ${node.op}`);
      }
    }
    default:
      throw new Error(`cannot evaluate node kind ${node.kind}`);
  }
}

module.exports = { evaluate };
