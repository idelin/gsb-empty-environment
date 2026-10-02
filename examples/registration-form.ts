import {
  email,
  matchesField,
  minLength,
  required,
  requiredWhen,
} from "../src/rules.ts";
import type { FormSchema } from "../src/form-validator.ts";

/**
 * Registration form showcasing the acceptance criteria:
 * - password / confirmPassword cross-field equality
 * - "married" checkbox dynamically adding a required rule to spouseName
 */
export const registrationSchema: FormSchema = {
  username: {
    rules: [required("用户名不能为空"), minLength(3, "用户名至少 3 个字符")],
  },
  email: {
    rules: [required("邮箱不能为空"), email()],
  },
  password: {
    rules: [required("密码不能为空"), minLength(8, "密码至少 8 个字符")],
  },
  confirmPassword: {
    rules: [
      required("请再次输入密码"),
      matchesField("password", "两次输入的密码不一致"),
    ],
  },
  married: {
    initial: false,
  },
  spouseName: {
    rules: [
      requiredWhen(
        (values) => values.married === true,
        ["married"],
        "已勾选已婚，配偶姓名必填",
      ),
    ],
  },
};
