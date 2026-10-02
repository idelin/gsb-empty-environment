import { FormValidator } from "../src/form-validator.ts";
import type { FieldState } from "../src/form-validator.ts";
import { registrationSchema } from "./registration-form.ts";

const form = new FormValidator(registrationSchema);

const render = (label: string): void => {
  const rows = Object.keys(registrationSchema).map((name) => {
    const state: FieldState = form.getState(name);
    const mark = state.valid ? "✓" : "✗";
    const errors = state.errors.length > 0 ? `  errors: ${state.errors.join("；")}` : "";
    return `  ${mark} ${name.padEnd(16)} = ${JSON.stringify(state.value)}${errors}`;
  });
  console.log(`\n=== ${label} ===`);
  console.log(rows.join("\n"));
  console.log(`  form valid: ${form.isValid()}`);
};

form.subscribe((event) => {
  if (event.type === "field") {
    console.log(
      `  [event] field "${event.field}" re-validated -> valid=${event.state.valid}` +
        (event.state.errors.length > 0 ? ` (${event.state.errors.join("；")})` : ""),
    );
  }
});

console.log("--- 1. 初始状态（动态规则已按 married=false 求值） ---");
render("initial");

console.log("\n--- 2. 填写密码与确认密码（一致） ---");
form.setValue("password", "S3cret!pass");
form.setValue("confirmPassword", "S3cret!pass");
render("passwords match");

console.log("\n--- 3. 修改密码 -> 确认密码被自动重新比对 ---");
form.setValue("password", "N3w!password");
render("password changed, confirm now mismatches");

console.log("\n--- 4. 同步确认密码 ---");
form.setValue("confirmPassword", "N3w!password");
render("confirm fixed");

console.log("\n--- 5. 勾选「已婚」-> spouseName 动态变为必填 ---");
form.setValue("married", true);
render("married = true");

console.log("\n--- 6. 填写配偶姓名 ---");
form.setValue("spouseName", "王芳");
render("spouse filled");

console.log("\n--- 7. 取消勾选「已婚」-> 必填规则即时移除 ---");
form.setValue("married", false);
render("married = false");

console.log("\n--- 8. 整表一键校验（其余字段仍为空） ---");
const ok = form.validateAll();
console.log(`  validateAll() -> ${ok}`);
render("after validateAll");

console.log("\n--- 9. 补全剩余字段后再次整表校验 ---");
form.setValue("username", "delin");
form.setValue("email", "delin@example.com");
console.log(`  validateAll() -> ${form.validateAll()}`);
render("all valid");
