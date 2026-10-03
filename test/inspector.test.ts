import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { ChangeFlow, makeRow, mulberry32 } from "../src/cdc.ts";
import { SyncInspector } from "../src/inspector.ts";
import { TableStore } from "../src/store.ts";

function makePair(partitionCount = 256) {
  const source = new TableStore("source", partitionCount);
  const target = new TableStore("target", partitionCount);
  return { source, target, inspector: new SyncInspector(source, target) };
}

function seedBoth(
  source: TableStore,
  target: TableStore,
  n: number,
  rand: () => number,
) {
  for (let i = 0; i < n; i++) {
    const row = makeRow(i, rand);
    source.put(row);
    target.put(structuredClone(row));
  }
}

describe("SyncInspector 在线巡检", () => {
  it("两端一致时巡检放行", () => {
    const { source, target, inspector } = makePair();
    seedBoth(source, target, 10_000, mulberry32(1));
    const report = inspector.patrol();
    assert.equal(report.inSync, true);
    assert.equal(report.mismatches.length, 0);
    assert.equal(report.rowsCompared, 0, "根哈希一致时不应做行级比对");
  });

  it("篡改目标库单行单字段 -> 精确定位主键与字段", () => {
    const { source, target, inspector } = makePair();
    seedBoth(source, target, 10_000, mulberry32(2));
    const victim = target.get("u777")!;
    target.put({ ...victim, balance: 0.01 }); // 悄悄篡改

    const report = inspector.patrol();
    assert.equal(report.inSync, false);
    assert.equal(report.mismatches.length, 1);
    assert.equal(report.mismatches[0].differences.length, 1);
    const diff = report.mismatches[0].differences[0];
    assert.equal(diff.pk, "u777");
    assert.equal(diff.kind, "tampered");
    assert.deepEqual(diff.changedFields, ["balance"]);
    // 下钻只扫描失配分区，行级比对量应远小于全表
    assert.ok(report.rowsCompared < 10_000 / 4);
  });

  it("复制丢数（目标库少行）-> 报 missing_in_target", () => {
    const { source, target, inspector } = makePair();
    seedBoth(source, target, 5_000, mulberry32(3));
    source.put({ id: "lost_row", v: 1 }); // 事件在链路上丢失
    const report = inspector.patrol();
    assert.equal(report.inSync, false);
    const diff = report.mismatches[0].differences[0];
    assert.equal(diff.pk, "lost_row");
    assert.equal(diff.kind, "missing_in_target");
  });

  it("目标库多出幽灵行 -> 报 missing_in_source", () => {
    const { source, target, inspector } = makePair();
    seedBoth(source, target, 5_000, mulberry32(4));
    target.put({ id: "ghost", v: 1 });
    const report = inspector.patrol();
    const diff = report.mismatches[0].differences[0];
    assert.equal(diff.pk, "ghost");
    assert.equal(diff.kind, "missing_in_source");
  });

  it("删除同步后恢复一致", () => {
    const { source, target, inspector } = makePair();
    seedBoth(source, target, 5_000, mulberry32(5));
    target.put({ id: "ghost", v: 1 });
    assert.equal(inspector.patrol().inSync, false);
    target.delete("ghost"); // 修复
    assert.equal(inspector.patrol().inSync, true);
  });

  it("验收场景：数十万条变更持续流动中篡改单行，当周期捕获并定位主键", () => {
    const rand = mulberry32(42);
    const source = new TableStore("source", 1024);
    const target = new TableStore("target", 1024);
    const inspector = new SyncInspector(source, target);
    seedBoth(source, target, 100_000, rand);

    const flow = new ChangeFlow(source, target, rand, 100_000);
    const BATCH = 10_000;
    const TAMPER_BATCH = 15; // 流动 15 万条变更后注入篡改
    let tamperedPk = "";
    let tamperAt = 0;
    let detection: { cycle: number; latencyMs: number; pk: string } | null =
      null;

    for (let b = 1; b <= 30; b++) {
      flow.applyBatch(BATCH); // 累计 30 万条变更
      if (b === TAMPER_BATCH) {
        tamperedPk = "u" + flow.pickLiveId();
        tamperAt = performance.now();
        flow.tamperOne(tamperedPk, "balance", -999.99);
      }
      const report = inspector.patrol();
      if (b < TAMPER_BATCH) {
        assert.equal(report.inSync, true, `批次 ${b} 不应误报`);
      }
      if (!report.inSync) {
        assert.ok(b >= TAMPER_BATCH, "篡改前不应报警");
        const all = report.mismatches.flatMap((m) => m.differences);
        assert.equal(all.length, 1, "应只报出被篡改的一行");
        assert.equal(all[0].kind, "tampered");
        assert.deepEqual(all[0].changedFields, ["balance"]);
        detection = {
          cycle: report.cycle,
          latencyMs: performance.now() - tamperAt,
          pk: all[0].pk,
        };
        break;
      }
    }

    assert.ok(detection, "必须在篡改后的当前巡检周期内发现");
    assert.equal(detection.pk, tamperedPk, "必须精确定位到被篡改主键");
    assert.ok(detection.latencyMs < 1000, "发现延迟应在毫秒级");
    const stats = flow.flowStats;
    assert.ok(
      stats.inserts + stats.updates + stats.deletes >= 150_000,
      "发现前已持续流动至少 15 万条变更",
    );
  });

  it("丢数与篡改并存时同周期全部报出", () => {
    const rand = mulberry32(7);
    const source = new TableStore("source", 256);
    const target = new TableStore("target", 256);
    const inspector = new SyncInspector(source, target);
    seedBoth(source, target, 20_000, rand);
    const flow = new ChangeFlow(source, target, rand, 20_000);

    flow.applyBatch(2_000, 0.01); // 约 20 条复制事件丢失
    const pk = "u" + flow.pickLiveId();
    flow.tamperOne(pk, "level", 99);

    const report = inspector.patrol();
    assert.equal(report.inSync, false);
    const kinds = new Set(
      report.mismatches.flatMap((m) => m.differences.map((d) => d.kind)),
    );
    assert.ok(kinds.has("tampered"));
    assert.ok(kinds.has("missing_in_target"));
    assert.ok(
      report.mismatches
        .flatMap((m) => m.differences)
        .some((d) => d.pk === pk && d.kind === "tampered"),
    );
  });
});
