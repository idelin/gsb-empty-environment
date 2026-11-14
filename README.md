# C++20 结构化审计日志

该组件供审计服务以 C++20 导入使用。每条事件被编码为一条 Canonical JSON
物理行，并通过序号、前一条哈希和 HMAC-SHA256 签名形成不可静默拆分或插入的链。

## 安全边界

- `AuditEvent` 只包含请求可控的 `action`、`object` 和 `outcome`。
- 主体与时间只来自 `ServerContextProvider`。服务必须用已认证会话和服务端时钟构造它，
  不得从请求体、请求头或客户端声明的 JSON 字段赋值。
- 所有值都作为 JSON 字符串编码：`"`、`\`、换行、回车、制表、C0/C1 控制字符、
  DEL、U+2028 和 U+2029 都会被转义。字段名与字段顺序固定，不能通过值添加、覆盖或
  重复顶层字段。
- `append()` 每次返回恰好一条以 `\n` 结尾的记录。值中的换行已经被转义为 `\\n`，
  因此一次调用不会产生第二条物理日志行。
- 记录哈希覆盖从 `version` 到 `prev_hash` 的 Canonical payload；签名使用
  HMAC-SHA256 覆盖同一段字节。下一记录的 `prev_hash` 指向前一记录的 `hash`。
- 校验器只接受其生成器产出的规范形式，并逐条检查序号、前序哈希、记录哈希和签名。

固定字段顺序如下：

```json
{"version":1,"sequence":1,"timestamp_ms":1700000000000,"principal":"srv:alice","action":"document.read","object":"report-42","outcome":"success","prev_hash":"<64 hex>","hash":"<64 hex>","signature":"<64 hex>"}
```

## 使用

```cpp
#include "netsec.hpp"

netsec::AuditLogger logger(
    server_secret,
    [&] {
        return netsec::ServerContext{
            .principal = authenticated_session.principal,
            .unix_time_ms = server_clock.now_unix_ms(),
        };
    });

// 只传请求可控字段；principal 与 timestamp 无法由客户端声明。
audit_stream << logger.append({
    .action = "document.delete",
    .object = request.target,
    .outcome = "denied",
});
```

验证完整日志：

```cpp
const netsec::VerificationResult result =
    netsec::verify_log(log_contents, server_secret);
if (!result.ok) {
    // result.failure、result.failed_record 和 result.message 给出失败位置与原因。
}
```

日志段续写与分段校验：

```cpp
netsec::AuditLogger resumed(
    server_secret, context_provider,
    previous_result.next_sequence, previous_result.last_hash);

auto segment = netsec::verify_log(
    new_records, server_secret,
    first_sequence, hash_immediately_before_segment);
```

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖换行/控制字符注入、伪造时间戳、字段覆盖与重复字段、合法事件、
序号/前序哈希/记录哈希/签名篡改、删除/插入/重排、错误密钥、链段锚点及独立
SHA-256/HMAC 参考向量。

## 部署注意

- 每个服务实例使用至少 32 字节的高熵签名密钥，并通过密钥管理系统注入。
- 日志文件应使用二进制/LF 模式写入，避免平台文本模式把 `\n` 改写为 CRLF。
- `append()` 返回完整记录行；并发场景应使用单写线程，或保证多个线程按
  `append()` 的调用顺序写出，否则物理文件顺序可能破坏链校验。
- 校验器能发现链中间的删除、插入和重排；要发现整个日志尾部被回滚截断，
  服务还需在外部可信位置保存并比对最后一个 `hash`/`sequence` 锚点。
