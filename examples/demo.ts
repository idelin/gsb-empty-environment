import { Reasoner } from "../src/horn.ts";

const r = new Reasoner();
r.addRule({ id: "r1", premises: ["A"], conclusion: "B" });
r.addRule({ id: "r2", premises: ["C"], conclusion: "B" });

const show = (label: string) => {
  console.log(`\n== ${label} ==`);
  console.log("当前事实:", r.facts().join(", ") || "(空)");
  const ex = r.explain("B");
  if (ex) {
    console.log(`B 的最短证据链 (深度 ${ex.depth}):`);
    console.log("  基础事实:", ex.baseFacts.join(", "));
    for (const s of ex.steps) {
      console.log(`  [${s.ruleId}] ${s.premises.join(" ∧ ") || "⊤"} ⊢ ${s.conclusion}`);
    }
  } else {
    console.log("B 不成立,无证据链。");
  }
};

r.assert("A");
show("断言 A(已知 A 与 A->B ⇒ 推出 B)");

r.assert("C");
show("再断言 C(B 现有 A、C 双重支持)");

r.retract("A");
show("撤销 A(B 因 C 独立支持而保留)");

r.retract("C");
show("撤销 C(B 失去全部支持,被安全清除)");
