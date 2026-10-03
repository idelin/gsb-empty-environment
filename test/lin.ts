import type { HistoryEntry, Operation, OpResult } from '../src/arbiter.ts';

export function checkRealTimeConsistency(history: readonly HistoryEntry[]): void {
  for (const a of history) {
    for (const b of history) {
      if (a.respondedAt < b.invokedAt && a.seq >= b.seq) {
        throw new Error(
          `real-time order violated: seq ${a.seq} responded before seq ${b.seq} invoked but serialized after`,
        );
      }
    }
  }
}

interface ModelCell {
  version: bigint;
  value: unknown;
}

function applyOp(state: Map<string, ModelCell>, op: Operation): OpResult {
  let cell = state.get(op.key);
  if (cell === undefined) {
    cell = { version: 0n, value: undefined };
    state.set(op.key, cell);
  }
  switch (op.kind) {
    case 'read':
      return { ok: true, conflict: false, version: cell.version, value: cell.value };
    case 'write':
      cell.version += 1n;
      cell.value = op.value;
      return { ok: true, conflict: false, version: cell.version, value: cell.value };
    case 'cas':
      if (cell.version !== op.expectedVersion) {
        return { ok: false, conflict: true, version: cell.version, value: cell.value };
      }
      cell.version += 1n;
      cell.value = op.value;
      return { ok: true, conflict: false, version: cell.version, value: cell.value };
  }
}

function resultEquals(a: OpResult, b: OpResult): boolean {
  return (
    a.ok === b.ok &&
    a.conflict === b.conflict &&
    a.version === b.version &&
    a.value === b.value
  );
}

function cloneState(state: Map<string, ModelCell>): Map<string, ModelCell> {
  const out = new Map<string, ModelCell>();
  for (const [k, v] of state) out.set(k, { ...v });
  return out;
}

export function isLinearizable(entries: readonly HistoryEntry[]): boolean {
  const ops = entries.filter((e) => !e.op.key.startsWith('<tx:'));
  const done = new Array<boolean>(ops.length).fill(false);
  const state = new Map<string, ModelCell>();

  const search = (remaining: number): boolean => {
    if (remaining === 0) return true;
    for (let i = 0; i < ops.length; i++) {
      if (done[i]) continue;
      let ready = true;
      for (let j = 0; j < ops.length; j++) {
        if (!done[j] && ops[j].respondedAt < ops[i].invokedAt) {
          ready = false;
          break;
        }
      }
      if (!ready) continue;
      const snapshot = cloneState(state);
      const expected = applyOp(state, ops[i].op);
      if (resultEquals(expected, ops[i].result)) {
        done[i] = true;
        if (search(remaining - 1)) return true;
        done[i] = false;
      }
      state.clear();
      for (const [k, v] of snapshot) state.set(k, v);
    }
    return false;
  };

  return search(ops.length);
}
