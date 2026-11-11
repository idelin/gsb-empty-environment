# 敏感字段端到端标记

网关在JSON重组后丢失敏感字段来源，日志脱敏因此漏掉嵌套值。写可导入的TypeScript带标记值库，构造、复制、数组合并与字段投影均保留标签，派生字符串标签取所有输入并集；只有白名单去标函数可移除指定标签。序列化按路径拒绝带禁止标签的值，并在写出前完成检查。用别名、循环、getter异常及共享子树测试拒绝时接收器未收到前缀。

## 用法

```ts
import { tag, clone, mergeArrays, project, deriveString, createUntagger } from "./tagged.ts";
import { writeJson, ForbiddenTagError } from "./serialize.ts";

const record = tag({ name: tag("alice", "pii"), password: tag("hunter2", "secret") }, "record");

project(record, ["name"]);            // 投影保留 "record" 与字段上的 "pii"
deriveString(tag("alice", "pii"), "@example.com"); // 派生字符串标签为输入并集
createUntagger(["pii"])(record, "pii"); // 仅白名单去标函数可移除指定标签（深移除）

writeJson(record, sink, { forbid: ["secret"] });
// 先完整检查整棵值树，发现禁止标签时抛 ForbiddenTagError（含 $.creds 式路径），
// sink 不会收到任何前缀；检查全部通过后才一次性写出。
```

运行测试：`npm test`
