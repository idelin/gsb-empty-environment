# 共享内存引用双取防护

可信应用要读取普通世界共享内存中的请求，攻击者控制普通世界内存，可在长度校验后修改，或给出 offset+size 回绕、落入安全内存区域的引用。为 C++17 可信应用写可嵌入的共享缓冲访问组件：引用先检查对齐、回绕和与安全区间不相交，数据只复制进安全内存一次，之后只使用副本；输出长度回写不得超过调用方声明。用注入的普通世界写入钩子在两次读取之间改数据，以及回绕和重叠引用验证。

## 组件

头文件、零外部依赖，可直接并入可信应用（TA）：

- `netsec/shared_buffer.hpp` — 全部实现，命名空间 `netsec`。
- `tests/test_shared_buffer.cpp` — 注入普通世界写入钩子的验证程序，由 CTest 运行。

### 威胁模型与保证

普通世界（NW）拥有共享内存的物理后端，可在任意时刻、包括 TA 两次读取之间
改写任意字节（TOCTOU）。组件因此强制：

1. 引用先校验后复制（`MemoryPolicy`，纯整数运算，不触碰 NW 内存）：
   - 对齐：引用首地址满足可配置的 2 的幂对齐要求；
   - 回绕：用 `__builtin_add_overflow` 检查 `base+offset`、`offset+size`、
     `base+size`，任何 64 位回绕都返回 `kWraparound`；
   - 窗口归属：引用必须完整落在配置的共享窗口内，否则 `kOutOfWindow`；
   - 安全区间不相交：与每个安全世界区间做半开区间重叠判断，重叠即
     `kOverlapsSecure`（区间恰好相接不算重叠）。
2. 单次快照复制（`SecureSnapshot`）：校验通过后 NW 数据只 `read` 一次进入
   安全世界缓冲，后续处理只用 `SecureSpan` 副本，绝不回读 NW；
   `takeInto` 提供零动态分配、写入调用方预留安全缓冲的变体。
3. 输出回写受限（`OutputWriter`）：输出窗口在 `bind` 时按同样规则校验一次；
   `write` 超出剩余声明容量即失败且不写任何字节，`writeClamped` 把长度钳制到
   调用方声明容量，回写永远不会越界。

所有 NW 访问都经由 `INormalWorldMemory` 抽象：真实 TA 可在其实现里接入 TEE
驱动与缓存维护；测试则注入带钩子的实现，在每次读取后篡改内存。

### 报文处理

`TaRequestHeader` 是 32 字节小端线格式示例。长度/偏移字段只从安全快照解码，
`loadLe32/loadLe64` 按字节解析，避免非对齐访问与严格别名问题；解码后这些值
即作为后续 `take` / `bind` 的唯一长度来源。

### 测试覆盖

`SimWorld` 模拟攻击者内存，支持读后写入钩子、访问故障注入与读写日志：

- 对齐、回绕（三类加法回绕）、越窗、超最大长度、安全区间重叠/相接；
- 载荷 TOCTOU：快照只读取一次且保持篡改前字节；附带一个刻意脆弱的两遍读取
  对照函数，证明钩子确实在两次读取之间改了数据（第一遍旧值、第二遍新值）；
- 报文头 TOCTOU：读头后钩子放大长度字段，TA 仍按安全副本中的原始长度取载荷；
  对照重读 NW 头可见被篡改的大长度；
- 定长安全缓冲容量不足、NW 访问故障；
- 输出写回：失败即不写、钳制长度、全部写操作落在声明窗口内、总写量不超容量；
- 端到端：读头 → 取载荷 → 绑定输出 → 受限回写。

### 构建与运行

```sh
cmake -S . -B build -G "Unix Makefiles" \
  -DCMAKE_AR=$(xcrun -f ar 2>/dev/null || echo /Library/Developer/CommandLineTools/usr/bin/ar)
cmake --build build
ctest --test-dir build --output-on-failure
```

测试默认开启 AddressSanitizer + UBSan，可用 `-DNETSEC_SANITIZE=OFF` 关闭。

### 集成注意

- 真实 `INormalWorldMemory::read/write` 应在复制前后做相应缓存清理/无效化，
  保证 TA 看到的是 NW 最新写入且回写对 NW 可见。
- 校验顺序固定为 对齐 → 回绕 → 窗口 → 安全区间；新增约束应保持“先拒绝、后
  复制”，任何失败路径都不得产生 NW 解引用或部分复制。
- `MemoryPolicy` 配置（窗口、安全区间、对齐、最大对象长度）来自 TA 自身的
  可信配置，绝不能取自 NW 输入。
