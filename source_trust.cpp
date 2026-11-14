#include "source_trust.hpp"

#include <algorithm>
#include <cmath>

namespace netsec::trust {

namespace {

double clamp01(double x) { return std::clamp(x, 0.0, 1.0); }

double median(std::vector<double> values) {
  const std::size_t n = values.size();
  std::nth_element(values.begin(), values.begin() + n / 2, values.end());
  const double hi = values[n / 2];
  if (n % 2 == 1) return hi;
  const double lo = *std::max_element(values.begin(), values.begin() + n / 2);
  return (lo + hi) / 2.0;
}

}  // namespace

FusionResult TrustFusion::fuse(const std::vector<Observation>& observations,
                               double now_s) {
  FusionResult result;

  if (observations.size() < cfg_.min_sources) {
    result.degraded = true;
    result.human_requested = true;
    result.reasons.push_back("insufficient sources: refuse silent accept");
    return result;
  }

  std::vector<double> values;
  values.reserve(observations.size());
  for (const auto& obs : observations) values.push_back(obs.value);
  const double center = median(values);  // 鲁棒中心，抗单源伪造

  bool any_jump = false;
  bool any_conflict = false;
  bool any_attack = false;
  double score_sum = 0.0;
  double weight_sum = 0.0;
  double fused_num = 0.0;

  result.sources.reserve(observations.size());
  for (const auto& obs : observations) {
    SourceReport report;
    report.kind = obs.kind;

    // 时间新鲜度：指数衰减
    const double age = std::max(0.0, now_s - obs.timestamp_s);
    const double w_time = std::exp(-age / cfg_.time_constant_s);

    // 历史一致性：与自身 EMA 的偏差，突变则标记并降权
    const int key = static_cast<int>(obs.kind);
    double w_consistency = 1.0;
    const auto it = ema_.find(key);
    if (it != ema_.end()) {
      const double dev = std::fabs(obs.value - it->second);
      w_consistency = 1.0 / (1.0 + std::pow(dev / cfg_.consistency_scale, 2.0));
      if (dev > cfg_.jump_threshold) {
        report.jump = true;
        any_jump = true;
      }
    }

    // 跨源冲突：偏离鲁棒中心则标记并降权
    double w_conflict = 1.0;
    if (std::fabs(obs.value - center) > cfg_.conflict_threshold) {
      report.conflict = true;
      any_conflict = true;
      w_conflict = cfg_.conflict_weight_factor;
    }

    // 攻击特征：直接进入评分惩罚
    double attack_factor = 1.0;
    if (!obs.attack_signatures.empty()) {
      report.attack = true;
      any_attack = true;
      const double penalty =
          std::min(cfg_.attack_penalty_cap,
                   cfg_.attack_penalty *
                       static_cast<double>(obs.attack_signatures.size()));
      attack_factor = 1.0 - penalty;
    }

    report.score = w_time * clamp01(obs.calibration) *
                   clamp01(obs.self_confidence) * w_consistency *
                   w_conflict * attack_factor;
    score_sum += report.score;
    weight_sum += report.score;
    fused_num += report.score * obs.value;
    result.sources.push_back(report);

    // 历史更新：带攻击特征的源不污染历史
    if (!report.attack) {
      double& e = ema_[key];
      e = (it == ema_.end()) ? obs.value
                             : cfg_.ema_alpha * obs.value +
                                   (1.0 - cfg_.ema_alpha) * it->second;
    }
  }

  result.aggregate_trust =
      score_sum / static_cast<double>(observations.size());
  result.fused_value = (weight_sum > 0.0) ? fused_num / weight_sum : center;
  for (auto& report : result.sources) {
    report.weight = (weight_sum > 0.0) ? report.score / weight_sum : 0.0;
  }

  // 降级判定：任何异常信号或信任度不足即进入降级态
  result.degraded = any_jump || any_conflict || any_attack ||
                    result.aggregate_trust < cfg_.accept_threshold;
  if (any_jump) result.reasons.push_back("single-source jump detected");
  if (any_conflict) result.reasons.push_back("cross-source conflict detected");
  if (any_attack) result.reasons.push_back("attack signature present");

  // 降级时提高保守阈值并请求人工；攻击特征存在时一律不接受
  const double threshold =
      result.degraded ? cfg_.conservative_threshold : cfg_.accept_threshold;
  const bool accept =
      !any_attack && result.aggregate_trust >= threshold;
  result.decision = accept ? Decision::Accept : Decision::RequireHuman;
  result.human_requested = result.degraded || !accept;
  if (!accept) {
    result.reasons.push_back(result.degraded
                                 ? "degraded: conservative threshold not met"
                                 : "trust below accept threshold");
  }
  return result;
}

}  // namespace netsec::trust
