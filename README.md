# 配对降级去掉了中间人保护

低功耗设备在配对时接受无中间人保护的关联模型，攻击者中间人获取长期密钥并冒充设备。请实现供C++20蓝牙栈调用的可导入配对策略组件。按设备能力选择配对方法，要求MITM时拒绝Just Works；密钥长度、绑定类型与IO能力逐项核对，降级请求拒绝。绑定信息绑定地址与身份密钥，重新配对使旧绑定失效。用降级协商、能力伪造、密钥替换、合法配对和重新绑定验证长期密钥不落入中间人。

## 组件说明

`netsec` 静态库（C++20）提供三个可导入的配对策略组件：

- `netsec::PairingPolicy`：按双方 IO 能力选择关联模型（`select_method`），并逐项核对密钥长度、绑定标志、MITM 与 Secure Connections 标志；任何降级请求或 MITM 要求下的 Just Works 均被拒绝（`evaluate`）。
- `netsec::BondStore`：绑定记录同时绑定对端地址与身份密钥（IRK）及 LTK；同一地址重新配对时旧绑定自动失效。
- `netsec::PairingSession`：驱动一次配对会话，仅在策略接受时才持久化 LTK，被拒绝（降级）的会话不会分发长期密钥。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Debug
cmake --build build
ctest --test-dir build --output-on-failure
```

测试覆盖五类场景：降级协商、能力伪造、密钥替换、合法配对与重新绑定，验证长期密钥不落入中间人。
