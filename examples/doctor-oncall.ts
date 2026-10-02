/**
 * 运行样例：医生值班表写倾斜检测
 *
 * 场景（验收边界）：
 *   医生 A、B 同时值班（值班人数 = 2，不变量：至少 1 人值班）。
 *   A 申请请假 —— 读到 B 在值班，认为可以离开；
 *   B 同时申请请假 —— 读到 A 在值班，认为可以离开。
 *   两事务并发提交时，系统在提交临界区重验不变量，
 *   发现值班人数将从 2 跌为 0，精准中止后提交的一方。
 */
import { Database, ConstraintViolationError } from "../src/index.ts";

async function main(): Promise<void> {
  // 初始化（同步写法便于演示）
  const db = new Database();
  db.addConstraint({
    name: "on_call.min_coverage",
    description: "值班表任意时刻至少有 1 名医生在岗",
    tables: ["on_call"],
    predicate: (view) =>
      view.count("on_call", (row) => row.onCall === true) >= 1,
  });
  const seed = db.begin();
  seed.put("on_call", "A", { doctor: "A", onCall: true });
  seed.put("on_call", "B", { doctor: "B", onCall: true });
  await seed.commit();
  db.checkConstraints();
  console.log("[初始化] A、B 同时在值班，值班人数 = 2，不变量成立\n");

  /**
   * 请假事务：读取当前值班名单 -> 应用层确认不止自己一人在岗 -> 把自己改为休息。
   * 注意：真正的防线不是这段应用层检查，而是提交期的约束重验。
   */
  async function requestLeave(doctor: string): Promise<string> {
    const tx = db.begin();
    const onCall = tx
      .scan("on_call")
      .filter((row) => row.onCall === true)
      .map((row) => row.doctor as string);
    console.log(
      `[${doctor}] 快照 v${tx.snapshotVersion} 读到值班名单: [${onCall.join(", ")}]`,
    );
    if (onCall.length <= 1) {
      tx.rollback();
      return `[${doctor}] 应用层检查：仅剩自己在岗，主动放弃请假`;
    }
    tx.put("on_call", doctor, { doctor, onCall: false });
    await tx.commit(); // <- 提交临界区内重验不变量，可能抛出 ConstraintViolationError
    return `[${doctor}] 请假提交成功`;
  }

  // 两个事务都先 begin（拿到同一快照 v1），再并发提交
  const txA = requestLeave("A");
  const txB = requestLeave("B");
  const results = await Promise.allSettled([txA, txB]);

  console.log("\n--- 提交结果 ---");
  for (const [i, r] of results.entries()) {
    const who = i === 0 ? "A" : "B";
    if (r.status === "fulfilled") {
      console.log(r.value);
    } else {
      const err = r.reason;
      if (err instanceof ConstraintViolationError) {
        console.log(`[${who}] 被系统精准中止 -> ${err.message}`);
      } else {
        throw err;
      }
    }
  }

  const finalOnCall = db
    .committedView()
    .scan("on_call")
    .filter((row) => row.onCall === true)
    .map((row) => row.doctor);
  console.log(`\n[最终状态] 值班名单: [${finalOnCall.join(", ")}]，人数 = ${finalOnCall.length}`);
  db.checkConstraints();
  console.log("[校验] 不变量「至少 1 人值班」在并发交叉修改后依然成立 ✔");
}

await main();
