#pragma once

#include <cstddef>
#include <string>
#include <unordered_map>
#include <vector>

// 来源可信度组件：供 C++20 感知融合模块调用。
// 对等权融合的替代——每源按时间新鲜度、标定健康度、自报置信度与
// 历史一致性加权；单源突变与跨源冲突会被标记并降权；攻击特征直接
// 进入评分；进入降级态时提高保守阈值并请求人工接管，绝不静默接受。
namespace netsec::trust {

enum class SensorKind { Camera, Radar, Lidar };

struct Observation {
  SensorKind kind;
  double value = 0.0;             // 观测量（如目标纵向距离，单位 m）
  double timestamp_s = 0.0;       // 采样时刻（秒，单调时钟）
  double self_confidence = 0.0;   // 传感器自报置信度 [0,1]
  double calibration = 0.0;       // 标定健康度 [0,1]
  std::vector<std::string> attack_signatures;  // 命中的攻击特征
};

enum class Decision { Accept, RequireHuman };

struct SourceReport {
  SensorKind kind;
  double weight = 0.0;   // 融合权重（归一化后）
  double score = 0.0;    // 原始可信度得分 [0,1]
  bool jump = false;     // 单源突变标记
  bool conflict = false; // 跨源冲突标记
  bool attack = false;   // 攻击特征标记
};

struct FusionResult {
  Decision decision = Decision::RequireHuman;
  double fused_value = 0.0;
  double aggregate_trust = 0.0;
  bool degraded = false;
  bool human_requested = false;
  std::vector<SourceReport> sources;
  std::vector<std::string> reasons;  // 决策依据，便于审计
};

struct TrustConfig {
  double time_constant_s = 0.5;        // 时间新鲜度衰减常数
  double jump_threshold = 5.0;         // 单源突变阈值（与历史 EMA 偏差，m）
  double consistency_scale = 2.0;      // 一致性加权尺度（m）
  double conflict_threshold = 3.0;     // 跨源冲突阈值（与鲁棒中心偏差，m）
  double conflict_weight_factor = 0.0; // 冲突源融合权重系数（0=剔除出融合）
  double attack_penalty = 0.3;         // 每条攻击特征的评分惩罚
  double attack_penalty_cap = 0.9;     // 攻击惩罚上限
  double accept_threshold = 0.45;      // 正常态接受阈值（聚合信任度）
  double conservative_threshold = 0.7; // 降级态保守阈值（更高）
  double ema_alpha = 0.3;              // 历史 EMA 更新系数
  std::size_t min_sources = 2;         // 最少可用源数
};

class TrustFusion {
 public:
  explicit TrustFusion(TrustConfig cfg = {}) : cfg_(cfg) {}

  // 对一批观测做可信度加权融合。now_s 为当前时刻（秒）。
  FusionResult fuse(const std::vector<Observation>& observations, double now_s);

  void reset() { ema_.clear(); }

 private:
  TrustConfig cfg_;
  std::unordered_map<int, double> ema_;  // 每源历史 EMA（键为 SensorKind）
};

}  // namespace netsec::trust
