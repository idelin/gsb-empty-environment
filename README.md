# 楼宇控制对象被写入了越界值

楼宇自控网关按对象标识转发写请求，攻击者用越界优先级或未授权对象改写冷冻机设定与报警阈值。请实现供C++20楼宇网关调用的可导入对象写保护组件。对象标识、类型、优先级与写入范围校验，关键设备只允许授权角色写；越界、未映射与优先级提升拒绝。写后回读确认。用越界写、优先级提升、对象伪造、合法写入和回读验证设定值不被篡改。

## 用法

```cpp
#include "netsec.hpp"

netsec::ObjectWriteProtector protector;
protector.register_object(
    netsec::ObjectPolicy{{netsec::ObjectType::ChillerSetpoint, 1},
                         5.0, 15.0, 1,
                         {netsec::Role::Admin, netsec::Role::Supervisor}, true},
    7.0);

auto result = protector.write(netsec::Role::Admin,
                              {netsec::ObjectType::ChillerSetpoint, 1},
                              /*priority=*/1, 9.5);
// result.ok == true 时 result.readback_value 为写后回读值；
// 否则 result.error 指示拒绝原因，原值保持不变。
```

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```
