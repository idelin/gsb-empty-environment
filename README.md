# .env文件NUL截断防护

应用加载用户提供的 .env 文件后，用 C 库接口设置进程环境，值中的 NUL 会让通过校验的字符串在下游被截断，LD_PRELOAD 之类的变量还能改变子进程行为。为Go的启动器写可导入的 dotenv 加载包：行格式为 `[export ]NAME=VALUE`，NAME 须匹配 [A-Za-z_][A-Za-z0-9_]*；VALUE 含 NUL（原字节，或 `\0` 转义还原后）拒绝整个文件；单引号值不做转义，双引号值支持 `\n`、`\\`、`\"`，多行双引号值合法；UTF-8 BOM 只允许出现在文件首部；名字属 LD_*、DYLD_*、BASH_ENV、ENV、IFS、PATH 或调用方给出的保护集合时拒绝，不是静默跳过；文件超过 64 KiB 或超过 512 行拒绝。测 NUL 截断、保护变量、BOM 位置、转义还原。

## 使用

```go
import "example.org/netsec/q0595/dotenv"

// 解析但不修改进程环境。
values, err := dotenv.ParseFile(".env", "EXTRA_PROTECTED")

// 完整校验通过后，再写入进程环境；任何错误都不会产生部分写入。
err = dotenv.LoadFile(".env", "EXTRA_PROTECTED")
```

根包 `example.org/netsec/q0595` 导出相同功能，包名为 `netsec`。
