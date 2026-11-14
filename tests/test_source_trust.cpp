#include "../source_trust.hpp"

#include <cmath>
#include <cstdio>
#include <string>

using namespace netsec::trust;

namespace {

int failures = 0;

void check(bool cond, const std::string& name) {
  if (cond) {
    std::printf("PASS  %s\n", name.c_str());
  } else {
    std::printf("FAIL  %s\n", name.c_str());
    ++failures;
  }
}

Observation good(SensorKind kind, double value, double now) {
  Observation o;
  o.kind = kind;
  o.value = value;
  o.timestamp_s = now;
  o.self_confidence = 0.95;
  o.calibration = 0.98;
  return o;
}

const SourceReport& report_of(const FusionResult& r, SensorKind kind) {
  for (const auto& s : r.sources)
    if (s.kind == kind) return s;
  std::abort();
}

// 场景 1：合法融合——三源一致、新鲜、高置信，应被接受且不请求人工
void test_legitimate_fusion() {
  TrustFusion fusion;
  const double now = 100.0;
  auto r = fusion.fuse({good(SensorKind::Camera, 10.1, now),
                        good(SensorKind::Radar, 10.0, now),
                        good(SensorKind::Lidar, 9.9, now)}, now);
  check(r.decision == Decision::Accept, "legit: accepted");
  check(!r.degraded, "legit: not degraded");
  check(!r.human_requested, "legit: no human requested");
  check(std::fabs(r.fused_value - 10.0) < 0.2, "legit: fused near 10");
  for (const auto& s : r.sources)
    check(!s.jump && !s.conflict && !s.attack, "legit: no flags");
}

// 场景 2：单源伪造——激光雷达被伪造到 50m，应被标记、降权，
// 融合结果不被带偏，且绝不静默接受
void test_single_source_spoof() {
  TrustFusion fusion;
  const double now = 100.0;
  auto r = fusion.fuse({good(SensorKind::Camera, 10.1, now),
                        good(SensorKind::Radar, 10.0, now),
                        good(SensorKind::Lidar, 50.0, now)}, now);
  check(report_of(r, SensorKind::Lidar).conflict, "spoof: lidar conflict flagged");
  check(!report_of(r, SensorKind::Camera).conflict, "spoof: camera clean");
  check(report_of(r, SensorKind::Lidar).weight <
            report_of(r, SensorKind::Camera).weight,
        "spoof: spoofed source down-weighted");
  check(std::fabs(r.fused_value - 10.0) < 1.0, "spoof: fusion not hijacked");
  check(r.decision == Decision::RequireHuman, "spoof: not silently accepted");
  check(r.degraded && r.human_requested, "spoof: degraded + human requested");
}

// 场景 3：跨源冲突——三源两两分裂，应标记冲突并拒绝静默接受
void test_cross_source_conflict() {
  TrustFusion fusion;
  const double now = 100.0;
  auto r = fusion.fuse({good(SensorKind::Camera, 10.0, now),
                        good(SensorKind::Radar, 14.0, now),
                        good(SensorKind::Lidar, 18.0, now)}, now);
  check(report_of(r, SensorKind::Camera).conflict &&
            report_of(r, SensorKind::Lidar).conflict,
        "conflict: outliers flagged");
  check(r.decision == Decision::RequireHuman, "conflict: not silently accepted");
  check(r.degraded && r.human_requested, "conflict: degraded + human requested");
}

// 场景 4：降级——攻击特征 + 过期数据 + 单源突变，应提高保守阈值、
// 请求人工，且即使信任度尚可也不静默接受
void test_degraded_mode() {
  TrustConfig cfg;
  TrustFusion fusion(cfg);
  const double t0 = 100.0;
  // 先建立历史基线
  fusion.fuse({good(SensorKind::Camera, 10.0, t0),
               good(SensorKind::Radar, 10.0, t0),
               good(SensorKind::Lidar, 10.0, t0)}, t0);

  const double now = t0 + 0.4;
  Observation attacked = good(SensorKind::Camera, 24.0, now);  // 突变
  attacked.attack_signatures = {"replay_timestamp", "invalid_signature"};
  Observation stale = good(SensorKind::Radar, 10.2, now - 5.0);  // 过期
  Observation ok = good(SensorKind::Lidar, 10.1, now);
  auto r = fusion.fuse({attacked, stale, ok}, now);

  check(report_of(r, SensorKind::Camera).jump, "degraded: jump flagged");
  check(report_of(r, SensorKind::Camera).attack, "degraded: attack flagged");
  check(report_of(r, SensorKind::Camera).conflict, "degraded: conflict flagged");
  check(r.degraded, "degraded: degraded mode entered");
  check(r.human_requested, "degraded: human requested");
  check(r.decision == Decision::RequireHuman, "degraded: not silently accepted");
  // 攻击特征进入评分：被攻击源得分显著低于正常源
  check(report_of(r, SensorKind::Camera).score <
            report_of(r, SensorKind::Lidar).score * 0.5,
        "degraded: attack signatures penalize score");
  // 保守阈值确实高于正常阈值
  check(cfg.conservative_threshold > cfg.accept_threshold,
        "degraded: conservative threshold raised");
}

// 边界：源不足时拒绝静默接受
void test_insufficient_sources() {
  TrustFusion fusion;
  auto r = fusion.fuse({good(SensorKind::Camera, 10.0, 100.0)}, 100.0);
  check(r.decision == Decision::RequireHuman, "insufficient: human required");
  check(r.human_requested, "insufficient: human requested");
}

}  // namespace

int main() {
  test_legitimate_fusion();
  test_single_source_spoof();
  test_cross_source_conflict();
  test_degraded_mode();
  test_insufficient_sources();
  if (failures == 0) {
    std::printf("ALL TESTS PASSED\n");
    return 0;
  }
  std::printf("%d CHECK(S) FAILED\n", failures);
  return 1;
}
