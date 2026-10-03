/**
 * demo.ts — 运行样例：
 *   1. 源库灌入 10 万行并全量复制到目标库；
 *   2. 启动持续变更流（30 万+ 条 INSERT/UPDATE/DELETE），巡检器逐周期比对；
 *   3. 中途人为篡改目标库某行单个字段 + 丢弃一条复制事件（丢数）；
 *   4. 巡检器在当前周期内捕获 Checksum 失配，并精确定位到主键 ID。
 *
 * 运行：node --experimental-strip-types src/demo.ts   （Node.js >= 22.6）
 */
import { ChangeFlow, makeRow, mulberry32 } from "./cdc.ts";
import { SyncInspector, type PatrolReport } from "./inspector.ts";
import { TableStore } from "./store.ts";

const PARTITIONS = 1024;
const SEED_ROWS = 100_000;
const TOTAL_CHANGES = 300_000;
const BATCH = 5_000;
const TAMPER_AT_BATCH = 37; // 在第 37 批变更后注入故障

function fmt(report: PatrolReport): string {
  if (report.inSync) {
    return `周期 #${report.cycle}: 一致 ✓ (根哈希比对 ${report.durationMs.toFixed(2)}ms)`;
  }
  const lines = [
    `周期 #${report.cycle}: 发现不一致 ✗ (耗时 ${report.durationMs.toFixed(2)}ms, 行级比对 ${report.rowsCompared} 行)`,
  ];
  for (const m of report.mismatches) {
    for (const d of m.differences) {
      if (d.kind === "tampered") {
        lines.push(
          `  分区 ${m.partition} | 主键 ${d.pk} | 篡改字段: ${d.changedFields!.join(", ")}` +
            ` | 源值=${JSON.stringify(d.sourceRow![d.changedFields![0]])} 目标值=${JSON.stringify(d.targetRow![d.changedFields![0]])}`,
        );
      } else {
        lines.push(
          `  分区 ${m.partition} | 主键 ${d.pk} | ${d.kind === "missing_in_target" ? "目标库丢数" : "目标库多行"}`,
        );
      }
    }
  }
  return lines.join("\n");
}

async function main(): Promise<void> {
  const rand = mulberry32(20261003);
  const source = new TableStore("source-db", PARTITIONS);
  const target = new TableStore("target-db", PARTITIONS);
  const inspector = new SyncInspector(source, target);

  console.log(`[1] 初始化：源库灌入 ${SEED_ROWS.toLocaleString()} 行并全量复制到目标库…`);
  let t0 = performance.now();
  const flow = new ChangeFlow(source, target, rand, SEED_ROWS);
  for (let i = 0; i < SEED_ROWS; i++) {
    const row = makeRow(i, rand);
    source.put(row);
    target.put(structuredClone(row));
  }
  console.log(
    `    完成，耗时 ${(performance.now() - t0).toFixed(0)}ms；源 ${source.size.toLocaleString()} 行 / 目标 ${target.size.toLocaleString()} 行`,
  );
  console.log(`    基线巡检 -> ${fmt(inspector.patrol())}`);

  console.log(
    `\n[2] 启动持续变更流：${TOTAL_CHANGES.toLocaleString()} 条变更（每批 ${BATCH.toLocaleString()} 条，批间巡检）…`,
  );
  console.log(`    计划故障注入：第 ${TAMPER_AT_BATCH} 批后篡改目标库单行单字段 + 丢弃一条复制事件\n`);

  let tamperedPk = "";
  let lostPk = "";
  let tamperAt = 0;
  let detected = false;
  const batches = Math.ceil(TOTAL_CHANGES / BATCH);

  for (let b = 1; b <= batches; b++) {
    flow.applyBatch(BATCH);

    if (b === TAMPER_AT_BATCH) {
      // 人为在目标库悄悄修改某行的单个字段
      tamperedPk = "u" + flow.pickLiveId();
      tamperAt = performance.now();
      flow.tamperOne(tamperedPk, "balance", 0.01);
      console.log(`    >>> 已注入故障：篡改目标库 ${tamperedPk} 的 balance 字段（绕过复制链路）`);
      // 人为丢弃一条复制事件（丢数）：源库已写入，目标库永远收不到
      flow.applyBatch(1, 1);
      const statsNow = flow.flowStats;
      lostPk = "u" + (SEED_ROWS + statsNow.inserts - 1);
      console.log(`    >>> 已注入故障：丢弃一条 INSERT 复制事件（主键 ${lostPk}，目标库丢数）`);
    }

    const report = inspector.patrol();
    if (!report.inSync) {
      const latency = performance.now() - tamperAt;
      console.log(`\n[3] 巡检捕获异常（故障注入后 ${latency.toFixed(2)}ms）：`);
      console.log(fmt(report));
      detected = true;
      break;
    }
    if (b % 10 === 0) {
      console.log(
        `    已流动 ${(b * BATCH).toLocaleString()} 条变更，巡检 ${report.cycle} 个周期全部一致（单周期 ${report.durationMs.toFixed(2)}ms）`,
      );
    }
  }

  const stats = flow.flowStats;
  console.log(`\n[4] 变更流统计：`);
  console.log(
    `    insert=${stats.inserts.toLocaleString()} update=${stats.updates.toLocaleString()} delete=${stats.deletes.toLocaleString()} 已复制=${stats.replicated.toLocaleString()} 丢失=${stats.dropped}`,
  );
  console.log(
    detected
      ? `\n结论：比对器在故障所在巡检周期内捕获 Checksum 失配，并定位到主键 ${tamperedPk}。`
      : `\n结论：未检测到异常（不符合预期！）`,
  );
  process.exit(detected ? 0 : 1);
}

await main();
