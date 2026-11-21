import { test } from "node:test";
import assert from "node:assert/strict";
import {
  auditBatch,
  buildSummary,
  commitBatch,
  deriveSampleIndices,
  issueNonce,
  samplingSeed,
  type EvidenceRecord,
} from "../src/evidence-audit.ts";

const COMMITTED_AT = "2026-11-20T10:00:00.000Z";
const AFTER_COMMIT = "2026-11-20T11:00:00.000Z";

function makeRecords(batchId: string, count: number, createdAt: string): EvidenceRecord[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `${batchId}-r${i}`,
    batchId,
    seq: i,
    amount: (i + 1) * 100,
    payload: `evidence-${i}`,
    createdAt,
  }));
}

test("一致批次：原始记录与汇总值一致，结论为 consistent，无不一致明细", () => {
  const records = makeRecords("batch-ok", 20, "2026-11-20T09:00:00.000Z");
  const commitment = commitBatch("batch-ok", records, COMMITTED_AT);
  const nonce = issueNonce(AFTER_COMMIT); // 随机数在承诺之后产生
  const summary = buildSummary("batch-ok", records);

  const report = auditBatch({
    commitment,
    declaredSummary: summary,
    currentRecords: records,
    nonce,
  });

  assert.equal(report.verdict, "consistent");
  assert.equal(report.discrepancies.length, 0);
  assert.ok(report.sampleSize >= 4); // 20 条记录默认抽 20%
  // 样本可由种子复算，任何一方都无法在承诺前预知
  const expected = deriveSampleIndices(samplingSeed(commitment, nonce), 20, report.sampleSize);
  assert.deepEqual(report.sampledIndices, expected);
});

test("补录批次：抽样后新增记录被逐条检出，结论为 backfilled，明细含记录标识", () => {
  const original = makeRecords("batch-backfill", 10, "2026-11-20T09:00:00.000Z");
  const commitment = commitBatch("batch-backfill", original, COMMITTED_AT);
  const nonce = issueNonce(AFTER_COMMIT);
  const summary = buildSummary("batch-backfill", original);

  // 抽样（承诺）之后补录 2 条记录
  const backfilled: EvidenceRecord[] = [
    { id: "batch-backfill-r10", batchId: "batch-backfill", seq: 10, amount: 1100, payload: "late-1", createdAt: "2026-11-20T12:00:00.000Z" },
    { id: "batch-backfill-r11", batchId: "batch-backfill", seq: 11, amount: 1200, payload: "late-2", createdAt: "2026-11-20T13:00:00.000Z" },
  ];

  const report = auditBatch({
    commitment,
    declaredSummary: summary,
    currentRecords: [...original, ...backfilled],
    nonce,
  });

  assert.equal(report.verdict, "backfilled");
  const details = report.discrepancies.filter((d) => d.type === "backfilled_record");
  assert.equal(details.length, 2);
  assert.deepEqual(
    details.map((d) => d.recordId).sort(),
    ["batch-backfill-r10", "batch-backfill-r11"],
  );
  // 补录之外无其他不一致，承诺前缀仍与汇总值一致
  assert.equal(report.discrepancies.filter((d) => d.type !== "backfilled_record").length, 0);
});

test("样本选取不可预知：随机数早于承诺时点时检出 nonce_predates_commitment", () => {
  const records = makeRecords("batch-nonce", 8, "2026-11-20T09:00:00.000Z");
  const commitment = commitBatch("batch-nonce", records, COMMITTED_AT);
  // 提交方在承诺前就已知随机数 -> 样本可预知
  const earlyNonce = issueNonce("2026-11-20T08:00:00.000Z");

  const report = auditBatch({
    commitment,
    declaredSummary: buildSummary("batch-nonce", records),
    currentRecords: records,
    nonce: earlyNonce,
  });

  assert.equal(report.verdict, "mismatch");
  assert.ok(report.discrepancies.some((d) => d.type === "nonce_predates_commitment"));
});

test("汇总值不一致：申报总额与原始记录重算结果不符时输出 summary_mismatch 明细", () => {
  const records = makeRecords("batch-sum", 6, "2026-11-20T09:00:00.000Z");
  const commitment = commitBatch("batch-sum", records, COMMITTED_AT);
  const nonce = issueNonce(AFTER_COMMIT);
  const actual = buildSummary("batch-sum", records);

  const report = auditBatch({
    commitment,
    declaredSummary: { ...actual, totalAmount: actual.totalAmount + 500 },
    currentRecords: records,
    nonce,
  });

  assert.equal(report.verdict, "mismatch");
  const detail = report.discrepancies.find((d) => d.type === "summary_mismatch");
  assert.ok(detail);
  assert.equal(detail.expected, String(actual.totalAmount + 500));
  assert.equal(detail.actual, String(actual.totalAmount));
});

test("承诺后篡改记录：被抽中样本无法锚定承诺根，输出 record_modified 明细", () => {
  const records = makeRecords("batch-tamper", 5, "2026-11-20T09:00:00.000Z");
  const commitment = commitBatch("batch-tamper", records, COMMITTED_AT);
  const nonce = issueNonce(AFTER_COMMIT);

  const tampered = records.map((r) => (r.seq === 2 ? { ...r, amount: 99999 } : r));
  const report = auditBatch({
    commitment,
    declaredSummary: buildSummary("batch-tamper", records),
    currentRecords: tampered,
    nonce,
    sampleSize: 5, // 全量抽样，确保被篡改记录必被抽中
  });

  assert.equal(report.verdict, "mismatch");
  const modified = report.discrepancies.filter((d) => d.type === "record_modified");
  assert.ok(modified.some((d) => d.recordId === "batch-tamper-r2"));
  assert.ok(report.discrepancies.some((d) => d.type === "root_mismatch"));
});
