import {
  and,
  asc,
  between,
  cmp,
  col,
  desc,
  exists,
  fn,
  inList,
  inQuery,
  isNull,
  join,
  not,
  or,
  param,
  scalar,
  select,
  subquery,
  table,
} from '../index.ts';
import type { IdentifierWhitelist, SelectNode } from '../src/index.ts';

export const whitelist: IdentifierWhitelist = {
  tables: [
    { name: 'users', columns: ['id', 'name', 'tenant_id', 'status', 'created_at'] },
    { name: 'orders', columns: ['id', 'user_id', 'amount', 'created_at', 'status'] },
    {
      schema: 'billing',
      name: 'invoices',
      columns: ['id', 'order_id', 'total', 'paid'],
    },
  ],
};

export function complexQuery(): SelectNode {
  const correlatedExists = select({
    selectList: [col('o', 'id')],
    from: table('orders', 'o'),
    where: cmp('=', col('o', 'user_id'), col('u', 'id')),
  });

  const derived = select({
    selectList: [
      col('user_id'),
      { expr: fn('SUM', [col('amount')]), as: 'spent' },
    ],
    from: table('orders'),
    groupBy: [col('user_id')],
  });

  return select({
    distinct: true,
    selectList: [
      col('u', 'id'),
      col('u', 'name'),
      { expr: fn('COALESCE', [col('u', 'status'), param('defaultStatus', 'string')]), as: 'state' },
    ],
    from: table('users', 'u'),
    joins: [
      join(
        'left',
        subquery(derived, 'totals'),
        cmp('=', col('totals', 'user_id'), col('u', 'id')),
      ),
    ],
    where: and(
      cmp('=', col('u', 'tenant_id'), param('tenantId', 'integer')),
      inList(col('u', 'status'), [
        param('statusA', 'string'),
        param('statusA', 'string'),
        param('statusB', 'string'),
      ]),
      or(cmp('>', col('totals', 'spent'), param('minSpent', 'number')), isNull(col('totals', 'spent'))),
      exists(correlatedExists),
      not(between(col('u', 'id'), param('lowId', 'integer'), param('highId', 'integer'), true)),
      inQuery(
        col('u', 'id'),
        select({
          selectList: [col('user_id')],
          from: table('orders'),
          where: cmp('>=', col('amount'), param('orderFloor', 'number')),
        }),
      ),
      not(isNull(col('u', 'created_at'))),
    ),
    groupBy: [col('u', 'tenant_id'), col('u', 'id'), col('u', 'name')],
    having: cmp('>', fn('COUNT', [col('u', 'id')], { distinct: true }), param('minCount', 'integer')),
    orderBy: [desc(col('u', 'id')), asc(col('u', 'name'))],
    limit: 100,
  });
}

export function emptyInQuery(): SelectNode {
  return select({
    selectList: [col('id')],
    from: table('users'),
    where: and(
      inList(col('status'), []),
      not(inList(col('name'), [], true)),
    ),
  });
}

export function scalarSubqueryQuery(): SelectNode {
  return select({
    selectList: [
      col('id'),
      {
        expr: cmp(
          '=',
          scalar(
            select({
              selectList: [fn('MAX', [col('total')])],
              from: table('invoices', 'i', 'billing'),
            }),
          ),
          param('cap', 'number'),
        ),
      },
    ],
    from: table('users'),
  });
}
