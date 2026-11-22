import type { AuthorView, MsgStatus } from './author-log.ts';
import type { SignedRecord } from './records.ts';

export interface ProofEntry {
  seq: bigint;
  digest: string | null;
  status: MsgStatus;
}

export interface Proof {
  author: string;
  unknownBelow: bigint;
  entries: ProofEntry[];
}

export interface StatusRange {
  from: bigint;
  to: bigint;
  status: MsgStatus;
  tipDigest: string | null;
}

export function buildProof(view: AuthorView): Proof {
  const entries: ProofEntry[] = [];
  for (const e of view.commonPrefix) {
    entries.push({ seq: e.seq, digest: e.digest, status: e.status });
  }
  const lifted = new Set(view.lifted);
  for (const d of view.lifted) {
    const rec = view.readable.find((r) => r.digest === d);
    if (rec) entries.push({ seq: rec.data.seq, digest: d, status: 'certain' });
  }
  for (const q of view.quarantined) {
    if (!lifted.has(q.digest)) entries.push({ seq: q.seq, digest: q.digest, status: 'quarantined' });
  }
  entries.sort((a, b) =>
    a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : (a.digest ?? '') < (b.digest ?? '') ? -1 : 1,
  );
  return { author: view.author, unknownBelow: view.unknownPrefixBelow, entries };
}

export function compressProof(proof: Proof): StatusRange[] {
  const ranges: StatusRange[] = [];
  const bySeq = new Map<bigint, ProofEntry[]>();
  for (const e of proof.entries) {
    let list = bySeq.get(e.seq);
    if (!list) bySeq.set(e.seq, (list = []));
    list.push(e);
  }
  const seqs = [...bySeq.keys()].sort((a, b) => (a < b ? -1 : 1));
  let run: ProofEntry[] = [];
  const flush = () => {
    if (run.length === 0) return;
    const last = run[run.length - 1];
    ranges.push({ from: run[0].seq, to: last.seq, status: last.status, tipDigest: last.digest });
    run = [];
  };
  for (const seq of seqs) {
    const list = bySeq.get(seq)!;
    if (list.length !== 1) {
      flush();
      for (const e of list) ranges.push({ from: seq, to: seq, status: e.status, tipDigest: e.digest });
      continue;
    }
    const e = list[0];
    const prev = run.length > 0 ? run[run.length - 1] : null;
    const mergeable =
      e.status === 'certain' &&
      e.digest !== null &&
      prev !== null &&
      prev.status === 'certain' &&
      e.seq === prev.seq + 1n;
    if (!mergeable) flush();
    if (mergeable || (e.status === 'certain' && e.digest !== null)) {
      run.push(e);
    } else {
      ranges.push({ from: seq, to: seq, status: e.status, tipDigest: e.digest });
    }
  }
  flush();
  return ranges;
}

export function expandRanges(
  ranges: StatusRange[],
  recordsByDigest?: Map<string, SignedRecord>,
): ProofEntry[] {
  const entries: ProofEntry[] = [];
  for (const r of ranges) {
    const count = Number(r.to - r.from + 1n);
    if (count === 1) {
      entries.push({ seq: r.from, digest: r.tipDigest, status: r.status });
      continue;
    }
    const digests: (string | null)[] = new Array<string | null>(count).fill(null);
    digests[count - 1] = r.tipDigest;
    if (recordsByDigest) {
      let d = r.tipDigest;
      for (let i = count - 1; i > 0 && d !== null; i--) {
        const rec = recordsByDigest.get(d);
        if (!rec) break;
        digests[i - 1] = rec.data.prev;
        d = rec.data.prev;
      }
    }
    for (let i = 0; i < count; i++) {
      entries.push({ seq: r.from + BigInt(i), digest: digests[i], status: r.status });
    }
  }
  entries.sort((a, b) =>
    a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : (a.digest ?? '') < (b.digest ?? '') ? -1 : 1,
  );
  return entries;
}
