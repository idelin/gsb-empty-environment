import { SchemaGraph, type SchemaDefinition } from '../src/schema.ts';

export const shopSchema: SchemaDefinition = [
  { name: 'User', primaryKey: ['id'], foreignKeys: [] },
  {
    name: 'Order',
    primaryKey: ['id'],
    foreignKeys: [
      { name: 'fk_order_user', columns: ['userId'], references: { table: 'User', columns: ['id'] } },
    ],
  },
  {
    name: 'OrderItem',
    primaryKey: ['id'],
    foreignKeys: [
      { name: 'fk_item_order', columns: ['orderId'], references: { table: 'Order', columns: ['id'] } },
    ],
  },
];

export function makeGraph(definition: SchemaDefinition = shopSchema): SchemaGraph {
  return new SchemaGraph(definition);
}
