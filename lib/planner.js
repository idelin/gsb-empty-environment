'use strict';
/** Compiled equivalent of src/planner.ts. */

const { exprToSql } = require('./decorrelate');

function buildPlan(result) {
  if (!result.outer) {
    throw new Error('cannot plan: query was not rewritten');
  }
  const outer = result.outer;
  let node = {
    op: 'SeqScan',
    table: outer.table,
    alias: outer.alias,
    children: [],
  };

  for (const join of result.joins) {
    let inner = {
      op: 'SeqScan',
      table: join.table,
      alias: join.alias,
      children: [],
    };
    if (join.filters.length > 0) {
      inner = { op: 'Filter', alias: join.alias, filters: join.filters, children: [inner] };
    }
    let op;
    if (join.keys.length > 0) {
      op = join.type === 'SEMI' ? 'HashSemiJoin' : 'HashAntiJoin';
    } else {
      op = join.type === 'SEMI' ? 'NestedLoopSemiJoin' : 'NestedLoopAntiJoin';
    }
    // children[0] = probe (outer) side, children[1] = build (inner) side
    node = { op, keys: join.keys, children: [node, inner] };
  }

  if (result.residual.length > 0) {
    node = { op: 'Filter', alias: outer.alias, residual: result.residual, children: [node] };
  }

  node = { op: 'Project', select: result.query.select, children: [node] };
  return node;
}

function explain(node, indent = '') {
  const lines = [];
  const head = (label) => {
    lines.push(`${indent}${label}`);
  };
  switch (node.op) {
    case 'SeqScan':
      head(`SeqScan on ${node.table} ${node.alias}`);
      break;
    case 'Filter': {
      const preds = (node.filters ?? node.residual ?? []).map(exprToSql).join(' AND ');
      head(`Filter: ${preds}`);
      break;
    }
    case 'HashSemiJoin':
    case 'HashAntiJoin':
    case 'NestedLoopSemiJoin':
    case 'NestedLoopAntiJoin': {
      const keys = (node.keys ?? []).map((k) => `${k.outer} = ${k.inner}`).join(', ');
      head(`${node.op}${keys ? `  [${keys}]` : ''}`);
      break;
    }
    case 'Project':
      head('Project');
      break;
  }
  for (const child of node.children) {
    lines.push(explain(child, indent + '  '));
  }
  return lines.join('\n');
}

module.exports = { buildPlan, explain };
