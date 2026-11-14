# 无人机指令授权组件

`netsec` 是供 C++20 飞控通信组件链接使用的指令授权库。它把无人机链路收到的航点与模式切换当作不可信输入，只有同时通过身份、密码学签名、会话、序列、操作员确认和独立安全边界的指令才会交给执行回调。

## 安全流程

1. 来源：命令的 `source_id` 必须是在本地注册的地面站。
2. 签名：地面站使用注册密钥对规范化命令计算 HMAC-SHA-256；消息标识、命令类型、会话、操作员、序列、时间、航点/模式都在签名范围内。
3. 会话：会话必须处于打开状态，并且绑定同一地面站与操作员；过期、关闭或身份不匹配的会话拒绝。
4. 序列：同一会话内序列严格递增；重复消息标识、重复序列和序列回滚都按重放拒绝。
5. 操作员确认：航点和模式切换都要求 `operator_confirmed`，且必须带有该地面站所属操作员的独立 HMAC 确认。
6. 独立安全复核：`SafetyValidator` 直接检查真实航点经纬度是否在地理围栏内、高度是否在限制内；模式切换检查允许列表。接收端自报的状态标志不能代替这些计算。
7. 执行门：`CommandGate::dispatch` 只有授权成功才调用回调，攻击流量不会触发飞控执行入口。

消息的 `priority` 字段不参与授权，也不能提升权限。攻击者即使设置最高优先级，仍需来源密钥、有效会话、递增序列和操作员确认。

## 使用

```cpp
#include "netsec.hpp"

netsec::SafetyLimits limits;
limits.geofence = {30.0, 32.0, 120.0, 122.0};
limits.altitude = {20.0, 100.0};

netsec::CommandAuthorizer authorizer(limits);
authorizer.register_ground_station("gcs-alpha", gcs_secret);
authorizer.register_operator("operator-7", "gcs-alpha", operator_secret);
authorizer.open_session(
    {"session-1", "gcs-alpha", "operator-7", session_expiry_ms});

netsec::CommandGate gate(
    authorizer,
    [&flight_control](const netsec::Command& command) {
        flight_control.execute(command);
    });

netsec::Command command;
command.message_id = "wp-101";
command.type = netsec::CommandType::Waypoint;
command.source_id = "gcs-alpha";
command.session_id = "session-1";
command.operator_id = "operator-7";
command.sequence = 101;
command.issued_at_ms = netsec::unix_time_ms();
command.operator_confirmed = true;
command.waypoint = {31.0, 121.0, 60.0};

netsec::sign_command(command, gcs_secret);
netsec::confirm_command(command, operator_secret);

const auto result = gate.dispatch(command);
if (!result) {
    // result.reason 给出拒绝原因，例如 InvalidSignature 或 OutsideGeofence
}
```

`open_session` 与会话密钥配置应在已经认证的链路握手之后调用，不应暴露为网络中可直接触发的控制接口。

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

测试覆盖：

- 未签名、最高优先级航点/降落注入拒绝且不执行；
- 签名后篡改航点、伪造来源和错误密钥拒绝；
- 原样重放、序列回滚、消息标识复用拒绝；
- 操作员确认缺失、错误确认、会话未知/过期/身份不匹配拒绝；
- 合法航点、合法模式切换按顺序执行；
- 围栏外、超高度、非有限坐标由独立安全复核拒绝。

## 部署注意

- 该库使用对称 HMAC 密钥。生产环境应通过安全密钥库/HSM 注入并轮换密钥，不应把密钥写进配置文件或日志。
- 序列与会话重放状态在当前进程内维护；跨重启防重放应配合持久化高水位或单调计数存储。
- 地理围栏目前采用经纬度矩形与高度区间，适合作为链路侧的第二道独立校验；任务规划器仍需执行更完整的航线、地形和空域检查。
- 对安全敏感的空域，建议在此基础上叠加链路加密、双向认证、时间新鲜度窗口和审计日志。
