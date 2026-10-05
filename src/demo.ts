import { WaitlistEngine } from "./waitlist-engine.ts";

function printState(title: string, engine: WaitlistEngine, sessionIds: string[]): void {
  console.log(`\n== ${title} ==`);
  for (const id of sessionIds) {
    console.log(
      `  场次 ${id}: 持有 [${engine.occupantsOf(id).join(", ") || "空"}] 候补 [${engine.waitlistOf(id).join(", ") || "空"}]`,
    );
  }
}

// ---------- 场景一：撤销引发级联递补 ----------
{
  const engine = new WaitlistEngine();
  engine.addSession("A", 1);
  engine.addSession("B", 1);
  engine.addSession("C", 1);

  engine.register({ id: "p1", priority: 100, preferences: ["A"] });
  engine.register({ id: "p2", priority: 90, preferences: ["A", "B"] });
  engine.register({ id: "p3", priority: 80, preferences: ["B", "C"] });
  engine.register({ id: "p4", priority: 70, preferences: ["B"] });
  engine.register({ id: "p5", priority: 60, preferences: ["C"] });

  console.log("【场景一】撤销一个名额，触发整条递补链");
  engine.settle();
  printState("首次结算", engine, ["A", "B", "C"]);

  engine.revoke("p1"); // p1 放弃 A，引发 p2 升级、释放 B、p3 升级、释放 C、p5 补上
  printState("p1 撤销 A 后（已自动递补至稳定）", engine, ["A", "B", "C"]);

  console.log("\n完整递补链:");
  for (const line of engine.formatChain()) console.log("  " + line);
}

// ---------- 场景二：撤销确认后按原候补资格恢复，而非重新排尾 ----------
{
  const engine = new WaitlistEngine();
  engine.addSession("X", 1);
  engine.addSession("Y", 1);

  engine.register({ id: "q1", priority: 100, preferences: ["X"] });
  engine.register({ id: "q2", priority: 90, preferences: ["X", "Y"] });
  engine.register({ id: "q3", priority: 90, preferences: ["Y"] });
  engine.register({ id: "q4", priority: 90, preferences: ["X"] }); // 与 q2 同优先级，但报名序号更靠后

  console.log("\n\n【场景二】撤销后恢复原候补位置");
  engine.settle();
  printState("首次结算", engine, ["X", "Y"]);

  engine.revoke("q2"); // q2 放弃 Y，按原序号回到 X 候补队列（排在 q4 之前）
  printState("q2 撤销 Y 后", engine, ["X", "Y"]);

  engine.revoke("q1"); // X 空出，应由恢复原位的 q2 补上，而非序号更靠后的 q4
  printState("q1 撤销 X 后", engine, ["X", "Y"]);

  console.log("\n完整递补链:");
  for (const line of engine.formatChain()) console.log("  " + line);
}
