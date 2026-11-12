# callgate：disable_functions 等价调用门

供解释器扩展的调用钩子调用：把每次函数调用的函数名、调用方式、回调
解析结果和参数交给 `Guard.Check`，返回是否放行及拒绝原因。

## 规范化与匹配

- `NormalizeName` 去前导反斜杠与命名空间前缀，`Class::method` 只保留
  方法名，统一小写后与禁用集合比较。
- 禁用集合：`system`、`exec`、`passthru`、`shell_exec`、`popen`、
  `proc_open`、`pcntl_exec`。
- 直接调用、可变函数、`call_user_func`、`array_map` 走同一套匹配逻辑。
- 字符串回调整体规范化；数组回调只取方法名；闭包与 `__invoke` 对象
  没有字符串名字，不参与名字匹配。
- `strlen` 等无关调用放行。

## putenv + mail 间接路径

- `putenv` 参数触碰 `LD_PRELOAD` 或 `LD_LIBRARY_PATH`（大小写不敏感，
  含删除形式）即把 Guard 置为污染，污染在 Guard 生命周期内粘性保持。
- 污染后再调用 `mail`、`mb_send_mail`、`imap_open` 一律拒绝，
  原因码为 `tainted_mailer`。

## 用法

```go
g := callgate.NewGuard()

// 钩子：普通 / 可变函数调用
g.Check(callgate.Call{Function: "SyStEm", Kind: callgate.KindDirect})
// -> Allowed:false, Reason:disabled_function, Matched:system

// 钩子：call_user_func / array_map 的字符串回调
g.Check(callgate.Call{
    Function: "call_user_func",
    Kind:     callgate.KindCallUserFunc,
    Callback: &callgate.Callback{Kind: callgate.CallbackString, Name: "Class::system"},
})
// -> Allowed:false, Reason:disabled_callback, Matched:system

// 钩子：putenv 之后 mail 被阻断
g.Check(callgate.Call{Function: "putenv", Args: []string{"LD_PRELOAD=evil.so"}})
g.Check(callgate.Call{Function: "mail"})
// -> Allowed:false, Reason:tainted_mailer, Matched:mail
```
