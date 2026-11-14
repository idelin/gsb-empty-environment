# 航空监视报文校验与航迹可信融合组件

`netsec.hpp` 是可直接包含的 C++20 头文件组件。航迹融合侧只需构造
`netsec::SurveillanceProcessor`，逐条调用 `process()` / `ingest()`，即可获得
结构化校验、可信度和安全标记。

## 快速使用

```cpp
#include "netsec.hpp"

netsec::SurveillanceProcessor fusion;

netsec::SurveillanceMessage adsb;
adsb.type = netsec::MessageType::AdsbPosition;
adsb.source = netsec::SourceType::Adsb;
adsb.flight_id = "UAL123";
adsb.icao_address = "ABC123";
adsb.latitude = 37.0;
adsb.longitude = -122.0;
adsb.altitude_ft = 35000.0;
adsb.timestamp = 1000.0;
adsb.receiver_id = "R1";
adsb.receiver_quality = 0.95;
adsb.signal_quality = 0.95;

auto report = fusion.process(adsb);
if (report.accepted) {
    // 进入航迹融合
}

netsec::SurveillanceMessage radar;
radar.type = netsec::MessageType::Radar;
radar.source = netsec::SourceType::Radar;
radar.flight_id = "UAL123";
radar.latitude = 37.0;
radar.longitude = -122.0;
radar.altitude_ft = 35000.0;
radar.timestamp = 1000.2;
radar.receiver_id = "RADAR1";

auto cross = fusion.process(radar);
// cross.authentic == true：ADS-B 与雷达位置/高度一致，目标通过交叉认证
```

可用别名包括 `TrackFusionComponent`、`TrackFusionEngine`、
`SurveillanceValidator`、`MessageAuthenticator`。所有报文都通过
`SurveillanceMessage`（别名 `Message`）传入。

## 报文校验

校验按 `MessageType` 分别执行：

- `AdsbPosition`：必须有位置与气压高度；校验经纬度、高度、速度/航向/垂直率包线。
- `AdsbVelocity`：必须有地速；校验速度、航向、垂直率及时间戳。
- `AdsbIdentification`：必须有机上标识；校验呼号字符集与长度。
- `AdsbSurface`：位置必需，高度可选。
- `ModeS`：必须有机载 24 位 ICAO 地址；可选位置/高度按存在字段校验。
- `Radar`：位置可由经纬度直接提供，也可由 `range_nm`、`azimuth_deg` 和接收机
  经纬度换算；高度可选，存在时参与交叉校验。
- 所有类型：时间戳必须有限且非负；ICAO 必须是有效 6 位十六进制地址；
  `crc_valid`（含 `crc_ok` / `valid_crc`）为假时直接拒收。

字段别名（`callsign`/`flight_id`、`icao24`/`icao_address`、`lat`/`latitude`、
`alt`/`altitude_ft`、`speed_kts`/`ground_speed_kts`、`ts`/`timestamp` 等）
会在归一化阶段统一，未知字段保持 `NaN`。`altitude_m` 会换算为英尺。

## 安全判定

结果在 `ValidationResult` 中同时提供布尔标记、`FlagSet`、可信度和状态：

| 标记 | 含义 |
| --- | --- |
| `IdentitySpoof` / `IdentityConflict` | 同一呼号被不同 ICAO 占用，或 ICAO 被不同呼号占用 |
| `PositionInjection` | 位置与已建立航迹或雷达观测矛盾 |
| `PositionJump` / `AltitudeJump` | 超出速度、垂直率与经过时间允许的运动学跳变 |
| `Replay` | 旧时间戳、序列回退或旧载荷在新航迹数据之后再次出现 |
| `Duplicate` | 同一接收机收到完全重复的报文 |
| `MultiSourceMismatch` / `AltitudeMismatch` | 多源位置或高度不一致 |
| `Uncorroborated` | 当前时间窗内没有独立接收机/雷达佐证 |
| `LowSignalQuality` / `LowReceiverQuality` / `LowCredibility` | 接收机或信号质量不足 |
| `OutOfEnvelope` | 位置、高度、速度、航向或垂直率超出配置包线 |
| `TrackConflict` | 雷达对较弱冲突源进行了纠正 |

可信度由接收机质量、信号质量/SNR/RSSI、CRC、独立源数量、雷达确认共同计算，
再按伪造、重放、跳变、交叉矛盾和低质量扣分。`authentic` 默认要求至少一个
独立源或雷达交叉确认，并且可信度达到 `authentic_threshold`；可通过
`SurveillanceConfig::permissive()` 放宽。

## 多源与雷达交叉

- 同一呼号 + ICAO 的报文归入同一航迹；雷达目标只有呼号时，若该呼号仅有一个
  候选航迹则关联到该航迹。
- `correlation_window_s` 内的不同 `source_id` / `receiver_id` 参与交叉校验。
- 位置误差超过 `radar_position_gate_nm`、高度误差超过 `altitude_gate_ft` 时
  标记多源矛盾；若单源 ADS-B 先被污染、随后雷达给出矛盾观测，雷达可纠正该
  弱证据航迹，避免攻击者先发报文造成航迹锁定。
- 同一载荷由不同接收机在短时间窗内收到属于正常多接收机观测，不会误报为回放；
  同一载荷跨时间窗复用或内容被修改则标记回放。

`assess_target("UAL123")` 返回 `TrackAssessment`，包含 `authentic`、`verified`、
`spoofed`、`radar_confirmed`、源数量、接收机数量和可信度。已知身份可通过
`register_identity("UAL123", "ABC123")` 预绑定；接收机质量可预先登记：

```cpp
fusion.register_receiver("R1", 0.95, true);
fusion.register_identity("UAL123", "ABC123", true);
```

## 构建与测试

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --parallel
ctest --test-dir build --output-on-failure
```

`tests/netsec_tests.cpp` 覆盖类型字段校验、包线越界、合法多源航迹、身份伪造、
位置注入、错误高度、重放、重复、跨接收机载荷复用、雷达纠正和接收机质量
对可信度的影响。
