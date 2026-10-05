// Independent integer interpreter used to check semantics.
// Division truncates toward zero; dividing by zero throws.

export function evaluate(node, env = {}) {
  switch (node.kind) {
    case 'num':
      return node.value;
    case 'ident': {
      if (!(node.name in env)) throw new Error(`undefined identifier: ${node.name}`);
      const v = env[node.name];
      if (typeof v !== 'number') throw new Error(`identifier is not a number: ${node.name}`);
      return v;
    }
    case 'unary':
      return -evaluate(node.arg, env);
    case 'binary': {
      const l = evaluate(node.left, env);
      const r = evaluate(node.right, env);
      switch (node.op) {
        case '+': return l + r;
        case '-': return l - r;
        case '*': return l * r;
        case '/':
          if (r === 0) throw new Error('division by zero');
          return Math.trunc(l / r);
        default: throw new Error(`unknown operator: ${node.op}`);
      }
    }
    case 'call': {
      const fn = env[node.name];
      if (typeof fn !== 'function') throw new Error(`undefined function: ${node.name}`);
      return fn(...node.args.map((a) => evaluate(a, env)));
    }
    case 'paren':
      return evaluate(node.expr, env);
    default:
      throw new Error(`unknown node kind: ${node.kind}`);
  }
}
