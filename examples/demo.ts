import { SchemaGraph, type SchemaDefinition } from '../src/schema.ts';
import { TargetDatabase } from '../src/target-db.ts';
import { ReplayScheduler, type CdcEvent } from '../src/scheduler.ts';

const schema: SchemaDefinition = [
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

const graph = new SchemaGraph(schema);
console.log('== FK topology ==');
console.log('insert order (parents first):', graph.topoOrder().join(' -> '));
console.log('delete order (children first):', graph.deleteOrder().join(' -> '));

const db = new TargetDatabase(graph);

const scrambledBatch: CdcEvent[] = [
  { id: 'e3', table: 'OrderItem', op: 'insert', row: { id: 100, orderId: 10, sku: 'book' } },
  { id: 'e2', table: 'Order', op: 'insert', row: { id: 10, userId: 1 } },
  { id: 'e1', table: 'User', op: 'insert', row: { id: 1, name: 'alice' } },
];

console.log('\n== Scrambled CDC batch (as arrived from the stream) ==');
for (const event of scrambledBatch) {
  console.log(`  ${event.id}: ${event.op} ${event.table}`, event.row ?? event.key);
}

const scheduler = new ReplayScheduler(graph, db, {
  concurrency: 4,
  onSuspend: (s) =>
    console.log(`  [suspend] ${s.op} ${s.table} (${s.eventId}) waiting for ${s.waitingFor.map((w) => `${w.table}${w.key}`).join(', ')}`),
  onRelease: (s) => console.log(`  [release] ${s.op} ${s.table} (${s.eventId}) dependencies satisfied`),
});

console.log('\n== Replay ==');
const insertReport = await scheduler.replay(scrambledBatch);
for (const record of insertReport.applied) {
  console.log(`  #${record.applyOrder} applied ${record.op} ${record.table} (${record.eventId})`);
}
console.log('target DB row counts:', {
  User: db.count('User'),
  Order: db.count('Order'),
  OrderItem: db.count('OrderItem'),
});

console.log('\n== Scrambled delete batch ==');
const deleteReport = await scheduler.replay([
  { id: 'd1', table: 'User', op: 'delete', key: '[1]' },
  { id: 'd2', table: 'Order', op: 'delete', key: '[10]' },
  { id: 'd3', table: 'OrderItem', op: 'delete', key: '[100]' },
]);
for (const record of deleteReport.applied) {
  console.log(`  #${record.applyOrder} applied ${record.op} ${record.table} (${record.eventId})`);
}
console.log('target DB row counts:', {
  User: db.count('User'),
  Order: db.count('Order'),
  OrderItem: db.count('OrderItem'),
});
console.log('\nDone: zero foreign-key violations raised by the target database.');
