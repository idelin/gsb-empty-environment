# Deno权限描述符包含与拒绝优先

Deno 式运行时按权限描述符决定脚本能读写哪些路径、连哪些主机、读哪些环境变量、运行哪些程序，攻击者控制的脚本会请求路径穿越、同前缀的另一个目录、通配环境变量，或借按命令名授权的运行权限执行同名的其他程序。为 TypeScript 宿主写供权限查询调用的判定包：输入允许与拒绝清单（read 与 write 的路径、net 的主机与可选端口、env 的变量名且可以 * 结尾、run 的命令名或绝对路径）和请求描述符；deny 命中即拒绝，优先于任何 allow；路径先规范化，/data 覆盖 /data/x 但不覆盖 /data2，.. 逃出清单目录拒绝，符号链接解析后落在拒绝目录内同样拒绝；net 清单项只写主机名时允许该主机全部端口，写了端口只允许该端口，IPv6 字面量带方括号；run 按命令名授权时，请求写成路径形式而解析结果不同则拒绝；查询整个目录时，清单里存在落在该目录下的 deny 项，则该目录不算已授权（本题规定）。用同前缀目录、.. 逃出、符号链接、通配变量和 deny 压过 allow 验证。

## 用法

```ts
import { checkPermission, decidePermission } from "./index.ts";

const lists = {
  allow: { read: ["/data"], net: ["example.com", "[::1]:8080"], env: ["AWS_*"], run: ["git"] },
  deny: { read: ["/data/secret"], net: ["example.com:8080"], env: ["AWS_SECRET_*"] },
};

checkPermission(lists, { kind: "read", path: "/data/x" });                // true
checkPermission(lists, { kind: "read", path: "/data2" });                 // false
checkPermission(lists, { kind: "net", host: "example.com", port: 8080 }); // false
decidePermission(lists, { kind: "env", name: "AWS_SECRET_KEY" });         // { granted: false, reason }
```

- 清单结构：`{ allow: {...}, deny: {...} }`，每类下为字符串数组；deny 命中即拒绝，优先于 allow。
- 请求描述符：`{ kind: "read"|"write", path, directory? }`、`{ kind: "net", host, port? }`、`{ kind: "env", name }`、`{ kind: "run", command }`。
- `checkPermission` 返回布尔；`decidePermission` 额外返回拒绝原因。
- 运行测试：`npm test`。
