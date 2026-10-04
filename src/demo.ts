import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { attachRecovery, buildCrashScenario } from "./scenario.ts";
import { DiskStorage } from "./storage.ts";

function showPages(label: string, dbPath: string): void {
  const disk = new DiskStorage(dbPath);
  const pages = ["P1", "P2", "P3", "P4"].map((p) => {
    const page = disk.readPage(p);
    return `${p}=${page?.data}(pageLSN=${page?.pageLSN})`;
  });
  console.log(`${label.padEnd(22)} ${pages.join("  ")}`);
}

const dir = mkdtempSync(join(tmpdir(), "aries-demo-"));
console.log(`scenario dir: ${dir}\n`);

const scenario = await buildCrashScenario(dir);
console.log("=== 崩溃前 (pre-crash) ===");
console.log(`已提交事务 (winners): T${scenario.txnIds.committed.join(", T")}`);
console.log(
  `未提交事务 (losers):  T${scenario.txnIds.uncommitted.join(", T")}, T${scenario.txnIds.abortingAtCrash}(崩溃时处于 ABORTING)`,
);
showPages("崩溃瞬间磁盘映像:", scenario.dbPath);

const { engine, wal } = await attachRecovery(scenario);
const report = await engine.recover();

console.log("\n=== Analysis 阶段 ===");
console.log("TransactionTable:", JSON.stringify(report.analysis.txnTable));
console.log("DirtyPageTable:  ", JSON.stringify(report.analysis.dirtyPageTable));

console.log("\n=== Redo 阶段 (历史重复) ===");
console.log(
  `fromLSN=${report.redo.fromLSN}  扫描=${report.redo.scanned}  重做=${report.redo.applied}  跳过=${report.redo.skipped}`,
);

console.log("\n=== Undo 阶段 (反向补偿) ===");
const winners =
  report.undo.winnerTxns.length > 0
    ? `T${report.undo.winnerTxns.join(",T")}`
    : "(无 — 已提交事务在崩溃前均已写 END)";
console.log(`losers=T${report.undo.loserTxns.join(",T")}  winners=${winners}`);
console.log(`写入 CLR=${report.undo.clrWritten}  写入 END=${report.undo.endWritten}`);

console.log("\n=== 恢复后 ===");
showPages("恢复后磁盘映像:", scenario.dbPath);
const expected = JSON.stringify(scenario.expected);
const disk = new DiskStorage(scenario.dbPath);
const actual = JSON.stringify(
  Object.fromEntries(["P1", "P2", "P3", "P4"].map((p) => [p, disk.readPage(p)?.data])),
);
console.log(`期望值: ${expected}`);
console.log(`实际值: ${actual}`);
console.log(actual === expected ? "\n✔ 恢复结果与期望一致" : "\n✘ 恢复结果不匹配");

console.log("\n=== 幂等性验证: 再次执行 recover() ===");
const walBefore = wal.allRecords().length;
const report2 = await engine.recover();
console.log(
  `第二次: redo.applied=${report2.redo.applied}  undo.clrWritten=${report2.undo.clrWritten}  新增日志=${wal.allRecords().length - walBefore}`,
);
showPages("二次恢复后磁盘:", scenario.dbPath);
console.log(
  report2.redo.applied === 0 && report2.undo.clrWritten === 0
    ? "✔ 二次恢复为完全无操作 (幂等)"
    : "✘ 二次恢复产生了额外工作",
);
