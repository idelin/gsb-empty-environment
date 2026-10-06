/**
 * Reference implementation: computes the converged state directly from a
 * message SET in one shot (no incremental logic). Used to cross-check the
 * incremental ListModel under every permutation of message delivery.
 */

import { compareIds, ROOT_ID } from '../src/list-model.js';
import type { InsertMsg, Message, SnapshotEntry } from '../src/list-model.js';

export interface RefDiagnostics {
  pending: string[];
  missingPredecessors: string[];
  deletesAwaitingInsert: string[];
}

interface Resolved<T> {
  inserts: Map<string, InsertMsg<T>>;
  deletes: Set<string>;
  included: Set<string>;
}

function resolve<T>(msgs: Array<Message<T>>): Resolved<T> {
  const inserts = new Map<string, InsertMsg<T>>();
  const deletes = new Set<string>();
  for (const msg of msgs) {
    if (msg.kind === 'insert') {
      if (!inserts.has(msg.id)) inserts.set(msg.id, msg);
    } else {
      deletes.add(msg.id);
    }
  }
  const included = new Set<string>();
  let changed = true;
  while (changed) {
    changed = false;
    for (const ins of inserts.values()) {
      if (included.has(ins.id)) continue;
      if (ins.pred === ROOT_ID || included.has(ins.pred)) {
        included.add(ins.id);
        changed = true;
      }
    }
  }
  return { inserts: inserts, deletes: deletes, included: included };
}

export function referenceSnapshot<T>(msgs: Array<Message<T>>): Array<SnapshotEntry<T>> {
  const r = resolve(msgs);
  const children = new Map<string, string[]>();
  for (const id of r.included) {
    const ins = r.inserts.get(id);
    if (ins === undefined) continue;
    let list = children.get(ins.pred);
    if (list === undefined) {
      list = [];
      children.set(ins.pred, list);
    }
    list.push(id);
  }
  for (const list of children.values()) {
    list.sort(compareIds);
  }
  const out: Array<SnapshotEntry<T>> = [];
  const walk = (pred: string): void => {
    const list = children.get(pred);
    if (list === undefined) return;
    for (const id of list) {
      const ins = r.inserts.get(id);
      if (ins === undefined) continue;
      out.push({ id: id, value: ins.value, deleted: r.deletes.has(id) });
      walk(id);
    }
  };
  walk(ROOT_ID);
  return out;
}

export function referenceDiagnostics<T>(msgs: Array<Message<T>>): RefDiagnostics {
  const r = resolve(msgs);
  const pending: string[] = [];
  const missing = new Set<string>();
  for (const ins of r.inserts.values()) {
    if (r.included.has(ins.id)) continue;
    pending.push(ins.id);
    if (ins.pred !== ROOT_ID && !r.included.has(ins.pred)) {
      missing.add(ins.pred);
    }
  }
  pending.sort(compareIds);
  const missingPredecessors = Array.from(missing);
  missingPredecessors.sort(compareIds);
  const deletesAwaitingInsert: string[] = [];
  for (const id of r.deletes) {
    if (!r.included.has(id)) deletesAwaitingInsert.push(id);
  }
  deletesAwaitingInsert.sort(compareIds);
  return {
    pending: pending,
    missingPredecessors: missingPredecessors,
    deletesAwaitingInsert: deletesAwaitingInsert,
  };
}
