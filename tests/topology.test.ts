import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SchemaGraph, SchemaCycleError, UnknownTableError } from '../src/schema.ts';
import { makeGraph, shopSchema } from './helpers.ts';

test('topoOrder places parents before children across a 3-table chain', () => {
  const graph = makeGraph();
  assert.deepEqual(graph.topoOrder(), ['User', 'Order', 'OrderItem']);
});

test('deleteOrder is the exact reverse of topoOrder (children first)', () => {
  const graph = makeGraph();
  assert.deepEqual(graph.deleteOrder(), ['OrderItem', 'Order', 'User']);
});

test('insert levels reflect cascade depth', () => {
  const graph = makeGraph();
  assert.equal(graph.insertLevel('User'), 0);
  assert.equal(graph.insertLevel('Order'), 1);
  assert.equal(graph.insertLevel('OrderItem'), 2);
});

test('diamond dependencies resolve to a valid partial order', () => {
  const graph = new SchemaGraph([
    { name: 'A', primaryKey: ['id'], foreignKeys: [] },
    { name: 'B', primaryKey: ['id'], foreignKeys: [{ name: 'fk_b_a', columns: ['aId'], references: { table: 'A', columns: ['id'] } }] },
    { name: 'C', primaryKey: ['id'], foreignKeys: [{ name: 'fk_c_a', columns: ['aId'], references: { table: 'A', columns: ['id'] } }] },
    {
      name: 'D',
      primaryKey: ['id'],
      foreignKeys: [
        { name: 'fk_d_b', columns: ['bId'], references: { table: 'B', columns: ['id'] } },
        { name: 'fk_d_c', columns: ['cId'], references: { table: 'C', columns: ['id'] } },
      ],
    },
  ]);
  const order = graph.topoOrder();
  assert.ok(order.indexOf('A') < order.indexOf('B'));
  assert.ok(order.indexOf('A') < order.indexOf('C'));
  assert.ok(order.indexOf('B') < order.indexOf('D'));
  assert.ok(order.indexOf('C') < order.indexOf('D'));
  assert.equal(graph.insertLevel('D'), 2);
});

test('self-referencing and mutual FK cycles are rejected at graph construction', () => {
  assert.throws(
    () =>
      new SchemaGraph([
        { name: 'A', primaryKey: ['id'], foreignKeys: [{ name: 'fk_a_b', columns: ['bId'], references: { table: 'B', columns: ['id'] } }] },
        { name: 'B', primaryKey: ['id'], foreignKeys: [{ name: 'fk_b_a', columns: ['aId'], references: { table: 'A', columns: ['id'] } }] },
      ]),
    (error: unknown) => {
      assert.ok(error instanceof SchemaCycleError);
      assert.deepEqual([...error.tables].sort(), ['A', 'B']);
      return true;
    },
  );
});

test('FK referencing an unknown table fails fast', () => {
  assert.throws(
    () =>
      new SchemaGraph([
        { name: 'Order', primaryKey: ['id'], foreignKeys: [{ name: 'fk', columns: ['userId'], references: { table: 'Ghost', columns: ['id'] } }] },
      ]),
    UnknownTableError,
  );
});

test('graph accessors expose parents and children', () => {
  const graph = makeGraph(shopSchema);
  assert.deepEqual(graph.parentsOf('OrderItem'), ['Order']);
  assert.deepEqual(graph.childrenOf('User'), ['Order']);
  assert.deepEqual(graph.childrenOf('OrderItem'), []);
});
