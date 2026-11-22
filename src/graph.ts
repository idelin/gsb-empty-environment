import { ZERO_DIGEST } from './encoding.ts';
import type { SignedRecord } from './records.ts';

export interface GraphView {
  heads: string[];
  forks: { seq: bigint; digests: string[] }[];
  commonTipDigest: string | null;
  commonTipSeq: bigint | null;
  prefixDigests: string[];
  statusByDigest: Map<string, 'certain' | 'quarantined'>;
}

export function interpretGraph(records: SignedRecord[]): GraphView {
  const byDigest = new Map(records.map((r) => [r.digest, r]));
  const referencedAsPrev = new Set(records.map((r) => r.data.prev));
  const heads = records
    .filter((r) => !referencedAsPrev.has(r.digest))
    .map((r) => r.digest)
    .sort();

  const bySeqPrev = new Map<string, Set<string>>();
  const seqOf = new Map<string, bigint>();
  for (const r of records) {
    const key = `${r.data.seq}:${r.data.prev}`;
    let s = bySeqPrev.get(key);
    if (!s) bySeqPrev.set(key, (s = new Set()));
    s.add(r.digest);
    seqOf.set(key, r.data.seq);
  }
  const forks = [...bySeqPrev]
    .filter(([, s]) => s.size > 1)
    .map(([key, s]) => ({ seq: seqOf.get(key)!, digests: [...s].sort() }))
    .sort((a, b) => (a.seq < b.seq ? -1 : 1));

  const chainMap = (head: string): Map<bigint, string> => {
    const map = new Map<bigint, string>();
    let d = head;
    while (d !== ZERO_DIGEST) {
      const rec = byDigest.get(d);
      if (!rec) break;
      map.set(rec.data.seq, d);
      d = rec.data.prev;
    }
    return map;
  };

  let commonTipDigest: string | null = null;
  let commonTipSeq: bigint | null = null;
  if (heads.length > 0) {
    const maps = heads.map(chainMap);
    for (const [seq, digest] of maps[0]) {
      if (maps.every((m) => m.get(seq) === digest) && (commonTipSeq === null || seq > commonTipSeq)) {
        commonTipSeq = seq;
        commonTipDigest = digest;
      }
    }
  }

  const prefixDigests: string[] = [];
  if (commonTipDigest !== null) {
    let d: string = commonTipDigest;
    while (d !== ZERO_DIGEST) {
      const rec = byDigest.get(d);
      if (!rec) break;
      prefixDigests.unshift(d);
      d = rec.data.prev;
    }
  }
  const prefixSet = new Set(prefixDigests);
  const statusByDigest = new Map<string, 'certain' | 'quarantined'>();
  for (const r of records) {
    statusByDigest.set(r.digest, prefixSet.has(r.digest) ? 'certain' : 'quarantined');
  }
  return { heads, forks, commonTipDigest, commonTipSeq, prefixDigests, statusByDigest };
}
