import type { CdcEvent, CompensationOp, RollbackPackage } from "./types.ts";
import { deleteSql, insertSql, updateSql } from "./sql.ts";

let packageCounter = 0;

/**
 * Generates reverse-compensation DML from CDC events.
 *
 * Inverse mapping per event:
 * - INSERT(after)          -> DELETE pk
 * - DELETE(before)         -> INSERT before-image
 * - UPDATE(before -> after)-> UPDATE ... SET before-image WHERE pk
 *
 * Events are compensated in reverse commit order so that chained changes
 * to the same row unwind correctly.
 */
export function generateRollbackPackage(
  events: readonly CdcEvent[],
  options: { pkColumn?: string; reason?: string; nowMs?: number } = {},
): RollbackPackage {
  const pkColumn = options.pkColumn ?? "id";
  const ops: CompensationOp[] = [];

  for (const event of [...events].sort((a, b) => b.seq - a.seq)) {
    ops.push(compensate(event, pkColumn));
  }

  return {
    id: `rb-${++packageCounter}`,
    createdAtMs: options.nowMs ?? Date.now(),
    reason: options.reason ?? "manual rollback",
    ops,
    coveredSeq:
      events.length === 0
        ? null
        : {
            from: Math.min(...events.map((e) => e.seq)),
            to: Math.max(...events.map((e) => e.seq)),
          },
  };
}

function compensate(event: CdcEvent, pkColumn: string): CompensationOp {
  switch (event.op) {
    case "INSERT":
      return {
        sourceSeq: event.seq,
        table: event.table,
        pk: event.pk,
        op: "DELETE",
        image: null,
        sql: deleteSql(event.table, pkColumn, event.pk),
      };
    case "DELETE": {
      if (event.before === null) {
        throw new Error(`CDC event seq=${event.seq}: DELETE missing before image`);
      }
      return {
        sourceSeq: event.seq,
        table: event.table,
        pk: event.pk,
        op: "INSERT",
        image: event.before,
        sql: insertSql(event.table, event.before),
      };
    }
    case "UPDATE": {
      if (event.before === null) {
        throw new Error(`CDC event seq=${event.seq}: UPDATE missing before image`);
      }
      return {
        sourceSeq: event.seq,
        table: event.table,
        pk: event.pk,
        op: "UPDATE",
        image: event.before,
        sql: updateSql(event.table, pkColumn, event.pk, event.before),
      };
    }
  }
}
