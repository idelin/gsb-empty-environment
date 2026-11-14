# USB 描述符一致性组件

该组件解析并校验 USB 设备、配置、接口、IAD 与端点描述符，并向 C++20
主机栈提供不可变的配置/备用设置视图。校验通过后，主机只能从同一个已解析镜像
生成活动端点集合；切换配置或备用设置会重新校验，失败时保留旧视图，不会出现
“校验一个描述符视图、实际通信使用另一个视图”的情况。

## API

```cpp
#include "netsec.hpp"

std::vector<std::uint8_t> image = /* device + configuration descriptor sets */;

auto imported = netsec::usb::HostDescriptorView::import(image);
if (!imported) {
    for (const auto& diagnostic : imported.diagnostics) {
        // diagnostic.error, diagnostic.offset, diagnostic.message
    }
}

auto host = std::move(*imported.view);
auto endpoints = host.activeEndpoints();       // 当前活动的唯一端点视图
auto switched = host.activateConfiguration(2); // 重新校验，成功才切换
```

`parseDescriptorImage` 用于只做离线校验；`HostDescriptorView` 用于主机栈的
枚举和运行时切换。`validateConfiguration` 可重新校验调用方构造的配置结构。

## 校验范围

- 设备/配置/接口/端点/IAD 的 `bLength`、描述符类型与固定字段。
- `wTotalLength` 边界、描述符重叠、截断、超长输入和描述符数量上限。
- 端点地址保留位、端点零、方向、传输类型以及按 USB 速度校验包大小。
- 接口端点数量、备用设置类字段一致性、跨接口端点地址冲突。
- IAD 接口范围、IAD 重叠、接口缺失以及可选的 IAD 类字段一致性。
- 同一配置中重复 `bConfigurationValue`、重复备用设置和重复端点。

默认速度未知，只执行与速度无关的包大小边界检查；调用方可通过
`ParseOptions::speed` 指定低速、全速、高速或超高速以启用严格检查。
`HostDescriptorView` 默认激活第一个配置，可通过
`autoActivateFirstConfiguration` 关闭。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖合法复合设备、双视图描述符、备用设置混淆、端点冲突、描述符重叠、
超长拒绝、配置切换以及切换失败时旧视图保持不变。
