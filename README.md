# netsec

面向 C++20 海事监控组件的船舶身份可信校验库。组件将静态身份报文与动态位置报文关联到同一 MMSI，维护有界航迹和来源观测窗口，并输出结构化告警与信任等级。

## 构建与测试

```sh
cmake -S . -B build
cmake --build build
ctest --test-dir build --output-on-failure
```

目标 `netsec::netsec` 可直接供其他 CMake 目标链接，公开接口位于 `netsec.hpp`。

## 快速使用

```cpp
#include "netsec.hpp"

netsec::ShipIdentityMonitor monitor;

netsec::StaticReport identity;
identity.message_type = netsec::MessageType::StaticVoyage; // AIS 5
identity.mmsi = "412345678";
identity.imo = "IMO9074729";
identity.name = "COSCO SHANGHAI";
identity.callsign = "BQAB";
identity.destination = "SHANGHAI";
identity.source_id = "rx-a";
identity.received_at = 1700000000;

netsec::DynamicReport position;
position.message_type = netsec::MessageType::ClassAPosition1; // AIS 1
position.mmsi = identity.mmsi;
position.latitude = 31.2304;
position.longitude = 121.4737;
position.speed_over_ground_knots = 12.0;
position.course_over_ground_degrees = 90.0;
position.true_heading_degrees = 90.0;
position.source_id = "rx-a";
position.observed_at = 1700000010;
position.received_at = 1700000010;

const auto static_result = monitor.ingest(identity);
const auto dynamic_result = monitor.ingest(position);

if (!dynamic_result.trusted) {
    for (const auto& finding : dynamic_result.findings) {
        // finding.code / finding.severity / finding.detail
    }
}
```

`Assessment::valid` 表示报文通过对应类型的字段校验；`accepted` 表示报文未被关键安全策略拒绝；`trust` 与 `trusted` 表示关联后的身份可信结论。即使报文不满足可信条件，也会返回具体 `FindingCode`，便于监控侧告警而不是丢弃证据。

## 报文校验

| 报文 | 必填/校验字段 |
| --- | --- |
| AIS 5 | 9 位船舶 MMSI、船名、呼号、目的地；可选 IMO 校验位 |
| AIS 24 | MMSI；Part A 必须有船名，Part B 必须有呼号；可按字段推断 Part |
| AIS 1/2/3/18/19/27 | MMSI、有效经纬度、SOG、COG、艏向；AIS 19 可携带船名 |

MMSI 必须是 9 位数字且 MID 在 201-775、首位为 2-7。经纬度必须是有限值并位于合法范围；`(0,0)` 视为未提供位置的哨兵。船名和目的地最多 20 个可打印 ASCII 字符，呼号最多 7 位字母数字。IMO 非空时校验 7 位及校验位。

静态与动态的交叉核对包括：

- AIS 19 或其他动态报文提供的船名、呼号、目的地声明必须与已关联静态档案一致。
- 静态报文晚于动态声明到达时，也会回查先前动态声明。
- 同一 MMSI 在冲突窗口内出现不同静态身份字段，标记 `IdentityConflict`。
- 动态报文没有静态档案时仍保留航迹，但结论为 `Unverified`。

## 策略与告警

默认策略位于 `netsec::Policy`，可按监控区域和接收网调整：

- 运动学跳变：超出 `max_plausible_speed_knots` 与 `position_noise_m` 包络时标记 `PositionJump`，异常点不进入航迹。
- 重复：同源同载荷落在 `duplicate_window_seconds` 内标记 `DuplicateMessage`（信息级）。
- 重放：旧观测在更新观测之后再次出现，或静态报文超过允许传输延迟，标记 `ReplayDetected`。
- 多源冲突：不同来源在 `source_sync_window_seconds` 内对同一 MMSI 给出超过 `multi_source_distance_m` 的位置差，标记 `MultiSourceConflict`。
- 时间窗：航迹按 `track_retention_seconds` 裁剪；超过 `track_gap_seconds` 后不做跨断档跳变判定，只标记 `TrackGap`。
- 静态档案：超过 `static_profile_ttl_seconds` 失效；身份字段冲突窗口由 `static_conflict_window_seconds` 控制。

关键告警使 `trust == Untrusted`；警告级告警使 `trust == Suspect`；无告警且存在当前静态档案时为 `Trusted`。无静态档案的动态航迹为 `Unverified`。

`ShipIdentityMonitor::profile()` 和 `track()` 可导出当前关联结果；数据按 MMSI 隔离，航迹和历史观测均有容量上限，避免长期运行无限增长。

## 验证场景

`tests/netsec_tests.cpp` 覆盖：

- 合法静态与动态报文关联、连续航迹；
- 标识冒用和静态/动态身份不一致；
- 不可达位置跳变；
- 重复报文与旧报文重放；
- 多源位置冲突及多源一致观测；
- 非法 MMSI、呼号、目的地、位置与速度；
- 航迹断档时间窗及 AIS 24 分片档案合并。
