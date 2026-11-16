# sudoedit的编辑器环境变量拆分与文件集合授权

sudoers 允许某用户用 sudoedit 编辑指定文件，sudoedit 会取环境变量里的编辑器命令并按空白拆分，攻击者把 EDITOR 设成“vim -- /etc/shadow”，让第二个文件在授权范围之外被编辑。为Go提权审计组件写可嵌入授权判定的 sudoedit 包，输入调用者环境（SUDO_EDITOR、VISUAL、EDITOR）、调用时给出的目标文件、sudoers 授权的文件集合和调用方给的编辑器白名单：编辑器取 SUDO_EDITOR，其次 VISUAL，再次 EDITOR 中第一个非空者；按空白拆分后首项为编辑器可执行文件，须是绝对路径且在白名单内，不得靠 PATH 查找；其后若出现 -- 或任何不以 - 开头的参数，视为额外文件，必须同样落在授权集合内，否则整次拒绝；所有目标文件按规范化路径与授权集合逐项比较，授权项以 / 结尾表示目录前缀，路径含 .. 段拒绝；目标文件或其任一父目录可被调用者写入或是符号链接时拒绝；授权判定在拆分环境变量之后进行，而不是之前。用 EDITOR 里带 --、符号链接父目录、.. 段、变量优先级和合法编辑验证。

## sudoedit 包

`sudoedit.Authorize` 可直接嵌入提权审计链路，在真正执行 sudoedit 前给出允许/拒绝判定：

```go
dec := sudoedit.Authorize(sudoedit.Request{
    Env:             sudoedit.EnvFromList(os.Environ()),
    Targets:         os.Args[1:],
    Authorized:      sudoersRules,          // "/etc/conf.d/" 为目录前缀
    EditorWhitelist: []string{"/usr/bin/vim", "/bin/nano"},
    Caller:          sudoedit.CurrentCaller(),
})
if !dec.Allow {
    // dec.Reason / dec.Detail / dec.File 可直接写入审计日志
}
```

判定顺序与要点：

1. 按 `SUDO_EDITOR` → `VISUAL` → `EDITOR` 取第一个非空值并按空白拆分，先拆分再授权。
2. 首项必须是绝对路径、不含 `..` 且命中白名单；相对名（依赖 PATH）直接拒绝。
3. 编辑器参数中裸 `--` 之后的全部参数、以及 `--` 之前不以 `-` 开头的参数都视为额外文件，与目标文件一起做授权与安全性检查。
4. 路径必须绝对且无 `..` 段；规范化后精确匹配授权文件，或落在以 `/` 结尾的目录前缀内（前缀按路径段匹配，`/etc/conf.d/` 不会匹配 `/etc/conf.d.evil`）。
5. 对文件本身及逐级父目录做 `lstat`：符号链接、调用者可写（含属主、属组、other 写位与补充组）一律拒绝；root 调用者 fail-closed。
6. `FS` 接口默认用 `OSFS` 走真实 `lstat`，测试时可注入内存文件系统。

测试：`go test ./...`（含 EDITOR 夹带 `--`、符号链接父目录、`..` 段、变量优先级、合法编辑放行等用例）。
