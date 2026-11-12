# pickle字节流安全解码

数据服务收到旧版客户端的 pickle 字节流，攻击者用 GLOBAL、STACK_GLOBAL、REDUCE 或 BUILD 让原实现执行任意调用。为Go服务实现可导入的安全解码包：只支持协议 2 至 5 的纯数据操作码（PROTO、FRAME、MARK、STOP、NONE、BININT1、BININT2、BININT、BINUNICODE、SHORT_BINUNICODE、EMPTY_LIST、EMPTY_DICT、EMPTY_TUPLE、APPEND、APPENDS、SETITEM、SETITEMS、TUPLE、BINPUT、BINGET、MEMOIZE），其余操作码立刻拒绝；memo 引用必须已定义，深度不超过 32，栈与 memo 各不超过 1 万项，STOP 后有多余字节拒绝。用 REDUCE 载荷、未定义 memo、深度超限和合法嵌套结构验证。
