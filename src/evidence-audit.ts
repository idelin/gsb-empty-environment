import { createHash, randomBytes } from "node:crypto";

// ---------- 数据模型 ----------

export interface EvidenceRecord {
  id: string;
  batchId: string;
  seq: number;
  amount: number;
  payload: string;
  createdAt: string; // ISO 时间戳
}

export interface BatchSummary {
  batchId: string;
  recordCount: number;
  totalAmount: number;
}

/** 提交方在抽样前登记的批次承诺，用于锚定"抽样时点"的证据状态 */
export interface BatchCommitment {
  batchId: string;
  merkleRoot: string;
  recordCount: number;
  totalAmount: number;
  committedAt: string;
}

/** 审计方在承诺登记之后才生成的随机数，保证样本选取不可被提交方预知 */
export interface AuditorNonce {
  value: string;
  issuedAt: string;
}

export type DiscrepancyType =
  | "summary_mismatch" // 汇总值与原始记录重算结果不一致
  | "record_modified" // 已承诺记录在抽样后被篡改
  | "backfilled_record" // 抽样后补录的记录
  | "root_mismatch" // 承诺前缀的 Merkle 根整体不一致
  | "nonce_predates_commitment"; // 随机数早于承诺，样本可被预知

export interface Discrepancy {
  type: DiscrepancyType;
  recordId?: string;
  seq?: number;
  expected: string;
  actual: string;
}

export type Verdict = "consistent" | "backfilled" | "mismatch";

export interface AuditReport {
  batchId: string;
  verdict: Verdict;
  sampleSize: number;
  sampledIndices: number[];
  discrepancies: Discrepancy[];
}

// ---------- 哈希与 Merkle 树 ----------

function sha256(data: string): string {
  return createHash("sha256").update(data, "utf8").digest("hex");
}

export function hashRecord(record: EvidenceRecord): string {
  return sha256(
    [record.id, record.batchId, record.seq, record.amount, record.payload, record.createdAt].join("|"),
  );
}

function hashPair(left: string, right: string): string {
  return sha256(left + right);
}

export function merkleRoot(leaves: string[]): string {
  if (leaves.length === 0) return sha256("");
  let level = leaves.slice();
  while (level.length > 1) {
    const next: string[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = i + 1 < level.length ? level[i + 1] : left;
      next.push(hashPair(left, right));
    }
    level = next;
  }
  return level[0];
}

export function merkleProof(leaves: string[], index: number): string[] {
  const proof: string[] = [];
  let level = leaves.slice();
  let idx = index;
  while (level.length > 1) {
    const sibling = idx % 2 === 0 ? idx + 1 : idx - 1;
    proof.push(sibling < level.length ? level[sibling] : level[idx]);
    const next: string[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const left = level[i];
      const right = i + 1 < level.length ? level[i + 1] : left;
      next.push(hashPair(left, right));
    }
    level = next;
    idx = Math.floor(idx / 2);
  }
  return proof;
}

export function verifyMerkleProof(leaf: string, proof: string[], index: number, root: string): boolean {
  let node = leaf;
  let idx = index;
  for (const sibling of proof) {
    node = idx % 2 === 0 ? hashPair(node, sibling) : hashPair(sibling, node);
    idx = Math.floor(idx / 2);
  }
  return node === root;
}

// ---------- 提交方：建批与登记承诺 ----------

export function buildSummary(batchId: string, records: EvidenceRecord[]): BatchSummary {
  const totalAmount = records.reduce((sum, r) => sum + r.amount, 0);
  return { batchId, recordCount: records.length, totalAmount };
}

export function commitBatch(
  batchId: string,
  records: EvidenceRecord[],
  committedAt: string,
): BatchCommitment {
  const ordered = sortRecords(records);
  const summary = buildSummary(batchId, ordered);
  return {
    batchId,
    merkleRoot: merkleRoot(ordered.map(hashRecord)),
    recordCount: summary.recordCount,
    totalAmount: summary.totalAmount,
    committedAt,
  };
}

function sortRecords(records: EvidenceRecord[]): EvidenceRecord[] {
  return records.slice().sort((a, b) => a.seq - b.seq);
}

// ---------- 审计方：抽样 ----------

export function issueNonce(now: string = new Date().toISOString()): AuditorNonce {
  return { value: randomBytes(32).toString("hex"), issuedAt: now };
}

/** 抽样种子 = H(batchId | 承诺根 | 审计随机数)，随机数在承诺之后才产生 */
export function samplingSeed(commitment: BatchCommitment, nonce: AuditorNonce): string {
  return sha256([commitment.batchId, commitment.merkleRoot, nonce.value].join("|"));
}

export function deriveSampleIndices(seed: string, populationSize: number, sampleSize: number): number[] {
  const size = Math.min(sampleSize, populationSize);
  const indices = new Set<number>();
  let counter = 0;
  while (indices.size < size) {
    const digest = sha256(`${seed}|${counter}`);
    indices.add(parseInt(digest.slice(0, 8), 16) % populationSize);
    counter += 1;
  }
  return [...indices].sort((a, b) => a - b);
}

export function defaultSampleSize(populationSize: number): number {
  if (populationSize <= 0) return 0;
  return Math.max(1, Math.ceil(populationSize * 0.2));
}

// ---------- 审计方：复核 ----------

export interface AuditInput {
  commitment: BatchCommitment;
  declaredSummary: BatchSummary;
  currentRecords: EvidenceRecord[];
  nonce: AuditorNonce;
  sampleSize?: number;
}

export function auditBatch(input: AuditInput): AuditReport {
  const { commitment, declaredSummary, nonce } = input;
  const discrepancies: Discrepancy[] = [];
  const ordered = sortRecords(input.currentRecords);
  const committedCount = commitment.recordCount;

  // 1. 样本选取不可预知性：随机数必须晚于承诺登记，样本可由种子复算
  if (nonce.issuedAt <= commitment.committedAt) {
    discrepancies.push({
      type: "nonce_predates_commitment",
      expected: `nonce.issuedAt > ${commitment.committedAt}`,
      actual: nonce.issuedAt,
    });
  }
  const seed = samplingSeed(commitment, nonce);
  const sampleSize = input.sampleSize ?? defaultSampleSize(committedCount);
  const sampledIndices = deriveSampleIndices(seed, committedCount, sampleSize);

  // 2. 补录检测：数量超出承诺、序号越界或创建时间晚于承诺时点
  const backfilled = ordered.filter(
    (r, i) => i >= committedCount || r.seq >= committedCount || r.createdAt > commitment.committedAt,
  );
  for (const record of backfilled) {
    discrepancies.push({
      type: "backfilled_record",
      recordId: record.id,
      seq: record.seq,
      expected: `committedAt ${commitment.committedAt} 前已存在且 seq < ${committedCount}`,
      actual: `seq=${record.seq}, createdAt=${record.createdAt}`,
    });
  }

  // 3. 一致性核对：仅针对承诺前缀（抽样时点已存在的记录）
  const prefix = ordered.slice(0, committedCount);
  const prefixLeaves = prefix.map(hashRecord);

  const recomputedSummary = buildSummary(commitment.batchId, prefix);
  if (recomputedSummary.totalAmount !== declaredSummary.totalAmount) {
    discrepancies.push({
      type: "summary_mismatch",
      expected: String(declaredSummary.totalAmount),
      actual: String(recomputedSummary.totalAmount),
    });
  }
  if (declaredSummary.recordCount !== committedCount) {
    discrepancies.push({
      type: "summary_mismatch",
      expected: `recordCount=${committedCount}`,
      actual: `recordCount=${declaredSummary.recordCount}`,
    });
  }

  const prefixRoot = merkleRoot(prefixLeaves);
  if (prefixRoot !== commitment.merkleRoot) {
    discrepancies.push({
      type: "root_mismatch",
      expected: commitment.merkleRoot,
      actual: prefixRoot,
    });
  }

  // 4. 抽样核对：被抽中记录须能凭 Merkle 证明锚定到承诺根
  for (const index of sampledIndices) {
    if (index >= prefix.length) continue;
    const record = prefix[index];
    const leaf = hashRecord(record);
    const proof = merkleProof(prefixLeaves, index);
    if (!verifyMerkleProof(leaf, proof, index, commitment.merkleRoot)) {
      discrepancies.push({
        type: "record_modified",
        recordId: record.id,
        seq: record.seq,
        expected: commitment.merkleRoot,
        actual: `leaf ${leaf} 无法锚定到承诺根`,
      });
    }
  }

  // 5. 结论
  const hasBackfill = discrepancies.some((d) => d.type === "backfilled_record");
  const hasHardMismatch = discrepancies.some((d) => d.type !== "backfilled_record");
  const verdict: Verdict = hasHardMismatch ? "mismatch" : hasBackfill ? "backfilled" : "consistent";

  return {
    batchId: commitment.batchId,
    verdict,
    sampleSize: sampledIndices.length,
    sampledIndices,
    discrepancies,
  };
}
