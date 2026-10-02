/**
 * 运行样例：写倾斜（Write Skew）场景下 纯SI vs SSI 的行为对比。
 * 运行：node src/demo.ts   （需要 Node >= 23.6，直接执行 TypeScript）
 */
import { Database, SerializationError } from "./ssi.ts";

function scenario(ssi: boolean, amount: number): void {
  const mode = ssi ? "SSI" : "纯SI";
  console.log(`\n=== ${mode} 模式：A=100, B=100, 约束 A+B>=0，T1 扣 A ${amount}，T2 扣 B ${amount} ===`);
  const db = new Database({ ssi });
  db.seed("A", 100);
  db.seed("B", 100);

  const t1 = db.begin();
  const t2 = db.begin();

  // T1：读 A、B，校验约束后扣 A。
  const a1 = t1.read("A")!;
  const b1 = t1.read("B")!;
  console.log(`T${t1.id} 读到 A=${a1}, B=${b1}（A+B=${a1 + b1}，扣 ${amount} 后仍 >=0）`);
  t1.write("A", a1 - amount);

  // T2：并发读 A、B（同一快照），校验约束后扣 B。
  const a2 = t2.read("A")!;
  const b2 = t2.read("B")!;
  console.log(`T${t2.id} 读到 A=${a2}, B=${b2}（A+B=${a2 + b2}，扣 ${amount} 后仍 >=0）`);
  t2.write("B", b2 - amount);

  t1.commit();
  console.log(`T${t1.id} 提交成功`);

  try {
    t2.commit();
    console.log(`T${t2.id} 提交成功`);
  } catch (err) {
    if (err instanceof SerializationError) {
      console.log(`T${t2.id} 被中止: ${err.message}`);
    } else {
      throw err;
    }
  }

  const finalA = db.committedValue("A")!;
  const finalB = db.committedValue("B")!;
  console.log(`最终状态: A=${finalA}, B=${finalB}, A+B=${finalA + finalB}`);
  console.log(finalA + finalB >= 0 ? "=> 约束 A+B>=0 成立" : "=> 约束 A+B>=0 被击穿（写倾斜异常）");
}

// 验收场景：扣 80。纯SI下双重扣款均生效（非可串行化交错）；SSI 精准中止 T2。
scenario(false, 80);
scenario(true, 80);

// 放大场景：扣 150。纯SI下约束被直接击穿（A+B=-100）；SSI 依然阻止。
scenario(false, 150);
scenario(true, 150);
