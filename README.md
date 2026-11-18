# pickle字节流安全解码

数据服务收到旧版客户端的 pickle 字节流，攻击者用 GLOBAL、STACK_GLOBAL、REDUCE 或 BUILD 让原实现执行任意调用。为Go服务实现可导入的安全解码包：只支持协议 2 至 5 的纯数据操作码（PROTO、FRAME、MARK、STOP、NONE、BININT1、BININT2、BININT、BINUNICODE、SHORT_BINUNICODE、EMPTY_LIST、EMPTY_DICT、EMPTY_TUPLE、APPEND、APPENDS、SETITEM、SETITEMS、TUPLE、BINPUT、BINGET、MEMOIZE），其余操作码立刻拒绝；FRAME 只在协议 4 以上出现且长度须精确覆盖后续操作码；memo 引用须已定义且索引在界内，STOP 后多余字节拒绝；栈操作前检查深度，APPEND 与 APPENDS 的目标类型须匹配，字典键须为不可变类型且不得重复；字符串按 UTF-8 严格解码，非法编码拒绝；深度不超过 32，栈与 memo 各不超过 1 万项，总操作码数量有上限；任何 GLOBAL、STACK_GLOBAL、REDUCE、BUILD 即使被 memo 引用也拒绝。用 REDUCE 载荷、未定义 memo、帧长不符、栈下溢、重复键与合法嵌套验证。
