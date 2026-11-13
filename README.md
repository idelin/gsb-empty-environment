# Windows批处理参数的二次解析注入

在 Windows 上执行 .bat 或 .cmd 时参数会被 cmd.exe 再解析一次，攻击者传 "&calc.exe 就能执行别的程序。为Go进程启动封装写可导入的参数准入包：目标按扩展名判定（含大小写、尾部空格与点、无扩展名按 PATHEXT 补全后为 bat 或 cmd），对这类目标出现 %、"、&、|、<、>、^、! 或换行的参数全部拒绝；其余可执行文件按 Windows C 运行库的反斜杠与引号规则转义。用 "&calc.exe、尾点文件名和普通 exe 的带空格参数验证。

## 用法

```go
cmd, err := netsec.SafeCommand(`C:\tools\run.bat`, `"&calc.exe`)
if errors.Is(err, netsec.ErrUnsafeArgument) {
    // 拒绝该参数，不启动子进程。
}

commandLine, err := netsec.EscapeCommandLine(
    `C:\Program Files\tool.exe`,
    []string{`C:\Program Files\input`},
)
```

- `.bat`、`.cmd`，以及无扩展名但能按 `PATHEXT` 补全为批处理的目标，使用严格字符准入。
- 目标扩展名比较忽略大小写，并兼容 Windows 路径尾部空格和点。
- 其他可执行文件使用 Windows C 运行库规则转义反斜杠和引号。
- `SafeCommand` 保留 `os/exec` 的原生 `argv` 传参方式，只负责启动前准入。
