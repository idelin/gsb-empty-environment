import { test } from 'node:test';
import assert from 'node:assert/strict';
import { InventoryLedger } from '../src/inventory.ts';

test('FEFO: earliest expiry first, inbound sequence breaks ties', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'B-late-expiry', qty: 10, expiry: '2027-01-01', eventTime: '2026-01-01T08:00:00Z' });
  ledger.receive({ batchId: 'B-early-2', qty: 5, expiry: '2026-06-01', eventTime: '2026-01-02T08:00:00Z' });
  ledger.receive({ batchId: 'B-early-1', qty: 5, expiry: '2026-06-01', eventTime: '2026-01-03T08:00:00Z' });

  const out = ledger.ship({ qty: 12, eventTime: '2026-02-01T00:00:00Z' });
  assert.deepEqual(out.allocations, [
    { batchId: 'B-early-2', qty: 5 },
    { batchId: 'B-early-1', qty: 5 },
    { batchId: 'B-late-expiry', qty: 2 },
  ]);
  assert.equal(out.shortage, 0);
});

test('late inbound never changes batch attribution of completed outbound', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'B1', qty: 10, expiry: '2027-06-01', eventTime: '2026-01-10T00:00:00Z' });

  const shipped = ledger.ship({ qty: 6, eventTime: '2026-02-01T00:00:00Z' });
  assert.deepEqual(shipped.allocations, [{ batchId: 'B1', qty: 6 }]);

  // Late arrival: physically received at T0 (before the shipment) with an
  // earlier expiry, but only recorded now. FEFO at ship time would have
  // picked it, yet the recorded outbound must stay attributed to B1.
  ledger.receive({ batchId: 'B0-late', qty: 10, expiry: '2027-01-01', eventTime: '2026-01-05T00:00:00Z' });

  const recorded = ledger.log.find((e) => e.kind === 'outbound');
  assert.ok(recorded && recorded.kind === 'outbound');
  assert.deepEqual(recorded.allocations, [{ batchId: 'B1', qty: 6 }]);

  // Historical view includes the late batch on hand, attribution untouched.
  const asOf = ledger.stockAsOf('2026-02-01T00:00:00Z');
  assert.equal(asOf.totals.onHand, 14);
  assert.equal(asOf.batches.find((b) => b.batchId === 'B0-late')?.onHand, 10);
  assert.equal(asOf.batches.find((b) => b.batchId === 'B1')?.onHand, 4);

  // New outbound after the late inbound is recorded does follow FEFO on B0.
  const next = ledger.ship({ qty: 8, eventTime: '2026-03-01T00:00:00Z' });
  assert.deepEqual(next.allocations, [{ batchId: 'B0-late', qty: 8 }]);
});

test('returns go back to the original batch and cannot exceed shipped qty', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'A', qty: 5, expiry: '2026-06-01', eventTime: '2026-01-01T00:00:00Z' });
  ledger.receive({ batchId: 'B', qty: 5, expiry: '2026-12-01', eventTime: '2026-01-01T00:00:00Z' });

  const out = ledger.ship({ qty: 7, eventTime: '2026-02-01T00:00:00Z' });
  assert.deepEqual(out.allocations, [
    { batchId: 'A', qty: 5 },
    { batchId: 'B', qty: 2 },
  ]);

  ledger.returnGoods({ batchId: 'A', qty: 4, eventTime: '2026-02-10T00:00:00Z' });

  const now = ledger.snapshot();
  assert.equal(now.batches.find((b) => b.batchId === 'A')?.onHand, 4);
  assert.equal(now.batches.find((b) => b.batchId === 'B')?.onHand, 3);
  assert.equal(now.totals.onHand, 7);

  // Historical: before the return, A was fully depleted.
  const before = ledger.stockAsOf('2026-02-05T00:00:00Z');
  assert.equal(before.batches.find((b) => b.batchId === 'A')?.onHand, 0);

  // Over-return beyond what A shipped is rejected.
  assert.throws(
    () => ledger.returnGoods({ batchId: 'A', qty: 2, eventTime: '2026-02-11T00:00:00Z' }),
    /exceeds shipped-but-not-returned/,
  );
  assert.throws(
    () => ledger.returnGoods({ batchId: 'UNKNOWN', qty: 1, eventTime: '2026-02-11T00:00:00Z' }),
    /unknown batch/,
  );
});

test('expired batches are quarantined and never allocated', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'E', qty: 10, expiry: '2026-01-01', eventTime: '2025-12-01T00:00:00Z' });
  ledger.receive({ batchId: 'F', qty: 5, expiry: '2027-01-01', eventTime: '2025-12-01T00:00:00Z' });

  // Before expiry, E is allocatable and preferred by FEFO.
  const early = ledger.ship({ qty: 4, eventTime: '2025-12-15T00:00:00Z' });
  assert.deepEqual(early.allocations, [{ batchId: 'E', qty: 4 }]);

  // After E expires, shipments only touch F; E's remaining 6 stay quarantined.
  const late = ledger.ship({ qty: 12, eventTime: '2026-06-01T00:00:00Z' });
  assert.deepEqual(late.allocations, [{ batchId: 'F', qty: 5 }]);
  assert.equal(late.shortage, 7);

  const report = ledger.stockAsOf('2026-06-01T00:00:00Z');
  const expiredE = report.batches.find((b) => b.batchId === 'E');
  assert.equal(expiredE?.expired, true);
  assert.equal(expiredE?.onHand, 6);
  assert.equal(report.totals.expired, 6);
  assert.equal(report.batches.find((b) => b.batchId === 'F')?.expired, false);
});

test('same-instant reservation competition conserves stock; cancel releases', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'S', qty: 10, expiry: '2027-01-01', eventTime: '2026-01-01T00:00:00Z' });

  const t = '2026-02-01T12:00:00Z';
  const r1 = ledger.reserve({ reservationId: 'r1', qty: 7, eventTime: t });
  const r2 = ledger.reserve({ reservationId: 'r2', qty: 7, eventTime: t });

  // First recorded wins; second gets only what remains. No over-allocation.
  assert.deepEqual(r1.allocations, [{ batchId: 'S', qty: 7 }]);
  assert.deepEqual(r2.allocations, [{ batchId: 'S', qty: 3 }]);

  const during = ledger.stockAsOf(t);
  assert.equal(during.totals.onHand, 10);
  assert.equal(during.totals.reserved, 10);
  assert.equal(during.totals.available, 0);
  // Conservation: reserved + available == onHand.
  assert.equal(during.totals.reserved + during.totals.available, during.totals.onHand);

  // A third reservation at the same instant finds nothing.
  const r3 = ledger.reserve({ reservationId: 'r3', qty: 1, eventTime: t });
  assert.deepEqual(r3.allocations, []);

  // Cancel releases the hold; stock becomes available again.
  ledger.cancelReservation({ reservationId: 'r1', eventTime: '2026-02-02T00:00:00Z' });
  const after = ledger.snapshot();
  assert.equal(after.totals.reserved, 3);
  assert.equal(after.totals.available, 7);
  assert.equal(after.totals.reserved + after.totals.available, after.totals.onHand);

  // Historical view between reserve and cancel still shows r1's hold.
  const between = ledger.stockAsOf('2026-02-01T18:00:00Z');
  assert.equal(between.totals.reserved, 10);

  // Cancelling twice (or an unknown id) is rejected.
  assert.throws(
    () => ledger.cancelReservation({ reservationId: 'r1', eventTime: '2026-02-03T00:00:00Z' }),
    /no active reservation/,
  );
});

test('reservations block outbound allocation until cancelled', () => {
  const ledger = new InventoryLedger();
  ledger.receive({ batchId: 'X', qty: 5, expiry: '2027-01-01', eventTime: '2026-01-01T00:00:00Z' });
  ledger.reserve({ reservationId: 'hold', qty: 5, eventTime: '2026-01-02T00:00:00Z' });

  const blocked = ledger.ship({ qty: 3, eventTime: '2026-01-03T00:00:00Z' });
  assert.deepEqual(blocked.allocations, []);
  assert.equal(blocked.shortage, 3);

  ledger.cancelReservation({ reservationId: 'hold', eventTime: '2026-01-04T00:00:00Z' });
  const freed = ledger.ship({ qty: 3, eventTime: '2026-01-05T00:00:00Z' });
  assert.deepEqual(freed.allocations, [{ batchId: 'X', qty: 3 }]);
});
