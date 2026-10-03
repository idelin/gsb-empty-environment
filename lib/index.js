'use strict';
/** Compiled equivalent of src/index.ts. */

const { parse, tokenize } = require('./parser');
const {
  splitConjuncts,
  collectColumns,
  decorrelate,
  exprToSql,
  selectToSql,
  renderRewrittenSql,
} = require('./decorrelate');
const { buildPlan, explain } = require('./planner');
const { newStats, evalExpr, executeBaseline, executePlan } = require('./engine');

function unnest(sql) {
  const stmt = parse(sql);
  const result = decorrelate(stmt);
  const rewritten = renderRewrittenSql(result);
  const plan = result.changed ? buildPlan(result) : null;
  return {
    original: sql,
    rewritten,
    changed: result.changed,
    result,
    plan,
    explain: plan ? explain(plan) : '(no rewrite applied)',
  };
}

module.exports = {
  tokenize,
  parse,
  splitConjuncts,
  collectColumns,
  decorrelate,
  exprToSql,
  selectToSql,
  renderRewrittenSql,
  buildPlan,
  explain,
  newStats,
  evalExpr,
  executeBaseline,
  executePlan,
  unnest,
};
