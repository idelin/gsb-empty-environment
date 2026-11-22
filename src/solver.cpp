#include "gsb/solver.hpp"

#include <algorithm>
#include <cstddef>
#include <limits>
#include <optional>
#include <set>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <utility>
#include <vector>

namespace gsb {

namespace {

// UINT64_MAX is reserved as the "no finite cover" sentinel.  Input costs of
// exactly UINT64_MAX are rejected by the loader.
constexpr uint64_t kInf = std::numeric_limits<uint64_t>::max();

struct RangeKey {
  size_t view = 0;
  uint64_t start = 0;
  uint64_t length = 0;

  bool operator==(const RangeKey& other) const {
    return view == other.view && start == other.start && length == other.length;
  }
};

struct RangeKeyHash {
  size_t operator()(const RangeKey& key) const {
    uint64_t h = std::hash<uint64_t>{}(static_cast<uint64_t>(key.view));
    auto mix = [&](uint64_t value) {
      h ^= value + 0x9e3779b97f4a7c15ull + (h << 6) + (h >> 2);
    };
    mix(key.start);
    mix(key.length);
    return static_cast<size_t>(h);
  }
};

struct BaseId {
  size_t page = 0;
  uint64_t offset = 0;
};

struct Witness {
  size_t export_index = 0;
  uint64_t position = 0;
};

struct QueryCaches {
  std::unordered_set<RangeKey, RangeKeyHash> no_leak;
  std::unordered_set<RangeKey, RangeKeyHash> no_mask;
};

class SolverImpl {
public:
  SolverImpl(const Model& model, uint64_t node_budget)
      : model_(model),
        secret_(computeSecretSet(model)),
        budget_(node_budget),
        items_by_view_(model.views.size()),
        reachable_(model.views.size(), 0),
        allowed_(model.items.size(), true) {
    for (size_t i = 0; i < model_.items.size(); ++i) {
      if (i >= 32) break;
      items_by_view_[model_.items[i].view].push_back(i);
      reachable_[model_.items[i].view] |= (1u << i);
    }
    for (size_t view = 0; view < model_.views.size(); ++view) {
      const ViewDef& def = model_.views[view];
      if (def.kind == ViewKind::Slice || def.kind == ViewKind::Reverse) {
        reachable_[view] |= reachable_[def.source];
      } else if (def.kind == ViewKind::Concat) {
        for (size_t part : def.parts) {
          reachable_[view] |= reachable_[part];
        }
      }
    }
  }

  SolveResult run() {
    SolveResult result;
    result.node_budget = budget_;

    // Secrecy is defined on base identities.  A keep position whose base byte
    // is secret can never be satisfied: it must be both masked and preserved.
    if (auto conflict = findSecretKeepOverlap()) {
      result.status = SolveStatus::Unsat;
      result.conflicts.push_back(std::move(*conflict));
      return result;
    }

    if (secret_.empty()) {
      result.status = SolveStatus::Optimal;
      result.total_cost = 0;
      result.nodes_explored = 0;
      return result;
    }

    filterItemsAgainstKeep();

    uint32_t forbidden = 0;
    for (size_t i = 0; i < model_.items.size(); ++i) {
      if (!allowed_[i]) forbidden |= (1u << i);
    }

    // Phase 1: exact minimum cost.
    MinContext root;
    root.banned = forbidden;
    uint64_t best_cost = minAdditional(0, root);
    result.nodes_explored = nodes_;
    if (budget_exceeded_) {
      result.status = SolveStatus::Undetermined;
      result.conflicts = conflicts_;
      appendItemConflicts(result);
      return result;
    }
    if (best_cost == kInf) {
      result.status = SolveStatus::Unsat;
      result.conflicts = conflicts_;
      appendItemConflicts(result);
      if (result.conflicts.empty()) {
        Conflict conflict;
        conflict.kind = "no_cover";
        conflict.detail = "no allowed combination of edit items covers all secret bytes";
        result.conflicts.push_back(std::move(conflict));
      }
      return result;
    }

    // Phase 2: lexicographically smallest sorted id list among all covers of
    // minimum cost.  At every position we choose the smallest next item that
    // still admits an optimal completion; skipped smaller ids are banned.
    uint32_t selected = 0;
    uint32_t banned = forbidden;
    uint64_t cost = 0;
    std::vector<size_t> chosen;
    while (true) {
      auto witness = findUncovered(selected);
      if (!witness) break;

      uint32_t step_tried = 0;
      bool picked = false;
      for (size_t i = 0; i < model_.items.size(); ++i) {
        uint32_t bit = (1u << i);
        if ((selected & bit) || (banned & bit) || !allowed_[i]) continue;
        uint64_t item_cost = model_.items[i].cost;
        if (item_cost > best_cost - cost) {
          step_tried |= bit;
          continue;
        }
        uint32_t trial_banned = banned | step_tried;
        MinContext ctx;
        ctx.banned = trial_banned;
        uint64_t rest = minAdditional(selected | bit, ctx);
        result.nodes_explored = nodes_;
        if (budget_exceeded_) {
          result.status = SolveStatus::Undetermined;
          result.conflicts = conflicts_;
          appendItemConflicts(result);
          result.has_best_known = !chosen.empty();
          for (size_t index : chosen) result.best_known.push_back(model_.items[index].id);
          result.best_known_cost = cost;
          return result;
        }
        uint64_t remaining = best_cost - cost - item_cost;
        if (rest == remaining) {
          chosen.push_back(i);
          selected |= bit;
          banned |= step_tried;
          cost += item_cost;
          picked = true;
          break;
        }
        step_tried |= bit;
      }
      if (!picked) {
        // The only way to get here is a numerical or structural inconsistency.
        result.status = SolveStatus::Undetermined;
        result.conflicts = conflicts_;
        appendItemConflicts(result);
        result.has_best_known = !chosen.empty();
        for (size_t index : chosen) result.best_known.push_back(model_.items[index].id);
        result.best_known_cost = cost;
        return result;
      }
    }

    result.status = SolveStatus::Optimal;
    result.total_cost = cost;
    result.selected.reserve(chosen.size());
    for (size_t index : chosen) result.selected.push_back(model_.items[index].id);
    result.nodes_explored = nodes_;
    return result;
  }

  // Validation helpers.  They use only selected bits and never enumerate a
  // huge expanded view.
  std::optional<uint64_t> checkMasked(size_t view, uint64_t start,
                                      uint64_t length, uint32_t selected) {
    QueryCaches caches;
    return findMasked(view, start, length, selected, caches);
  }

  std::optional<std::pair<size_t, uint64_t>> checkUncovered(uint32_t selected) {
    auto witness = findUncovered(selected);
    if (!witness) return std::nullopt;
    return std::make_pair(witness->export_index, witness->position);
  }

private:
  struct MinContext {
    uint32_t banned = 0;
    std::unordered_map<uint32_t, uint64_t> memo;
  };

  bool touchesSecret(size_t view, uint64_t start, uint64_t length) {
    if (length == 0) return false;
    RangeKey key{view, start, length};
    auto it = touches_cache_.find(key);
    if (it != touches_cache_.end()) return it->second;

    const ViewDef& def = model_.views[view];
    bool result = false;
    switch (def.kind) {
      case ViewKind::PageSlice:
        result = secret_.intersects(def.page, def.page_offset + start,
                                    def.page_offset + start + length);
        break;
      case ViewKind::Slice:
        result = touchesSecret(def.source, def.source_offset + start, length);
        break;
      case ViewKind::Reverse:
        result = touchesSecret(def.source, def.length - (start + length), length);
        break;
      case ViewKind::Concat: {
        uint64_t end = start + length;
        size_t part = static_cast<size_t>(
            std::upper_bound(def.prefix.begin(), def.prefix.end(), start) -
            def.prefix.begin());
        if (part > 0) --part;
        while (part < def.parts.size() && def.prefix[part] < end) {
          uint64_t part_start = def.prefix[part];
          uint64_t part_end = def.prefix[part + 1];
          uint64_t lo = std::max(start, part_start);
          uint64_t hi = std::min(end, part_end);
          if (lo < hi &&
              touchesSecret(def.parts[part], lo - part_start, hi - lo)) {
            result = true;
            break;
          }
          ++part;
        }
        break;
      }
    }
    touches_cache_.emplace(key, result);
    return result;
  }

  std::optional<uint64_t> findSecretPosition(size_t view, uint64_t start,
                                             uint64_t length) {
    if (length == 0 || !touchesSecret(view, start, length)) return std::nullopt;
    const ViewDef& def = model_.views[view];
    switch (def.kind) {
      case ViewKind::PageSlice: {
        uint64_t base_start = def.page_offset + start;
        uint64_t base_end = base_start + length;
        const std::vector<PageRange>* ranges = secret_.ranges(def.page);
        if (!ranges) return std::nullopt;
        auto it = std::lower_bound(
            ranges->begin(), ranges->end(), base_start,
            [](const PageRange& range, uint64_t value) { return range.end <= value; });
        for (; it != ranges->end() && it->start < base_end; ++it) {
          uint64_t lo = std::max(it->start, base_start);
          uint64_t hi = std::min(it->end, base_end);
          if (lo < hi) return lo - def.page_offset;
        }
        return std::nullopt;
      }
      case ViewKind::Slice: {
        auto p = findSecretPosition(def.source, def.source_offset + start, length);
        if (!p) return std::nullopt;
        return *p - def.source_offset;
      }
      case ViewKind::Reverse: {
        auto p = findSecretPosition(def.source, def.length - (start + length),
                                    length);
        if (!p) return std::nullopt;
        return def.length - 1 - *p;
      }
      case ViewKind::Concat: {
        uint64_t end = start + length;
        size_t part = static_cast<size_t>(
            std::upper_bound(def.prefix.begin(), def.prefix.end(), start) -
            def.prefix.begin());
        if (part > 0) --part;
        while (part < def.parts.size() && def.prefix[part] < end) {
          uint64_t part_start = def.prefix[part];
          uint64_t part_end = def.prefix[part + 1];
          uint64_t lo = std::max(start, part_start);
          uint64_t hi = std::min(end, part_end);
          if (lo < hi) {
            auto p = findSecretPosition(def.parts[part], lo - part_start, hi - lo);
            if (p) return part_start + *p;
          }
          ++part;
        }
        return std::nullopt;
      }
    }
    return std::nullopt;
  }

  std::vector<std::pair<uint64_t, uint64_t>> directCovered(
      size_t view, uint64_t start, uint64_t length, uint32_t selected) {
    std::vector<std::pair<uint64_t, uint64_t>> covered;
    if (length == 0) return covered;
    uint64_t end = start + length;
    for (size_t item_index : items_by_view_[view]) {
      if ((selected & (1u << item_index)) == 0) continue;
      const Interval& interval = model_.items[item_index].interval;
      uint64_t lo = std::max(start, interval.start);
      uint64_t hi = std::min(end, interval.end());
      if (lo < hi) covered.emplace_back(lo, hi);
    }
    std::sort(covered.begin(), covered.end());
    std::vector<std::pair<uint64_t, uint64_t>> merged;
    for (const auto& range : covered) {
      if (merged.empty() || range.first > merged.back().second) {
        merged.push_back(range);
      } else if (range.second > merged.back().second) {
        merged.back().second = range.second;
      }
    }
    return merged;
  }

  std::optional<uint64_t> pageLeak(
      const ViewDef& def, uint64_t start, uint64_t length,
      const std::vector<std::pair<uint64_t, uint64_t>>& covered) {
    uint64_t base_start = def.page_offset + start;
    uint64_t base_end = base_start + length;
    const std::vector<PageRange>* ranges = secret_.ranges(def.page);
    if (!ranges) return std::nullopt;
    auto it = std::lower_bound(
        ranges->begin(), ranges->end(), base_start,
        [](const PageRange& range, uint64_t value) { return range.end <= value; });
    for (; it != ranges->end() && it->start < base_end; ++it) {
      uint64_t cur = std::max(it->start, base_start);
      uint64_t limit = std::min(it->end, base_end);
      for (const auto& [lo, hi] : covered) {
        uint64_t cover_lo = def.page_offset + lo;
        uint64_t cover_hi = def.page_offset + hi;
        if (cover_hi <= cur) continue;
        if (cover_lo >= limit) break;
        if (cover_lo > cur) {
          return cur - def.page_offset;
        }
        if (cover_hi > cur) cur = cover_hi;
        if (cur >= limit) break;
      }
      if (cur < limit) return cur - def.page_offset;
    }
    return std::nullopt;
  }

  std::optional<uint64_t> findLeak(size_t view, uint64_t start, uint64_t length,
                                   uint32_t selected, QueryCaches& caches) {
    if (length == 0 || !touchesSecret(view, start, length)) return std::nullopt;
    RangeKey key{view, start, length};
    if (caches.no_leak.contains(key)) return std::nullopt;

    if ((reachable_[view] & selected) == 0) {
      auto p = findSecretPosition(view, start, length);
      if (!p) caches.no_leak.insert(key);
      return p;
    }

    auto covered = directCovered(view, start, length, selected);
    bool full = false;
    if (covered.size() == 1 && covered[0].first == start &&
        covered[0].second == start + length) {
      full = true;
    }
    if (full) {
      caches.no_leak.insert(key);
      return std::nullopt;
    }

    const ViewDef& def = model_.views[view];
    if (def.kind == ViewKind::PageSlice) {
      auto p = pageLeak(def, start, length, covered);
      if (!p) caches.no_leak.insert(key);
      return p;
    }

    uint64_t end = start + length;
    uint64_t cursor = start;
    auto handle = [&](uint64_t a, uint64_t b) -> std::optional<uint64_t> {
      if (a >= b || !touchesSecret(view, a, b - a)) return std::nullopt;
      if (def.kind == ViewKind::Slice) {
        auto p = findLeak(def.source, def.source_offset + a, b - a, selected, caches);
        if (p) return *p - def.source_offset;
      } else if (def.kind == ViewKind::Reverse) {
        auto p = findLeak(def.source, def.length - b, b - a, selected, caches);
        if (p) return def.length - 1 - *p;
      } else {
        size_t part = static_cast<size_t>(
            std::upper_bound(def.prefix.begin(), def.prefix.end(), a) -
            def.prefix.begin());
        if (part > 0) --part;
        while (part < def.parts.size() && def.prefix[part] < b) {
          uint64_t part_start = def.prefix[part];
          uint64_t part_end = def.prefix[part + 1];
          uint64_t lo = std::max(a, part_start);
          uint64_t hi = std::min(b, part_end);
          if (lo < hi) {
            auto p = findLeak(def.parts[part], lo - part_start, hi - lo, selected,
                              caches);
            if (p) return part_start + *p;
          }
          ++part;
        }
      }
      return std::nullopt;
    };

    for (const auto& [lo, hi] : covered) {
      if (cursor < lo) {
        if (auto p = handle(cursor, lo)) return p;
      }
      if (hi > cursor) cursor = hi;
      if (cursor >= end) break;
    }
    if (cursor < end) {
      if (auto p = handle(cursor, end)) return p;
    }
    caches.no_leak.insert(key);
    return std::nullopt;
  }

  std::optional<Witness> findUncovered(uint32_t selected) {
    QueryCaches caches;
    for (size_t export_index = 0; export_index < model_.exports.size();
         ++export_index) {
      const ExportDef& export_def = model_.exports[export_index];
      const ViewDef& view = model_.views[export_def.view];
      if (view.length == 0) continue;
      auto p = findLeak(export_def.view, 0, view.length, selected, caches);
      if (p) return Witness{export_index, *p};
    }
    return std::nullopt;
  }

  std::optional<uint64_t> findMasked(size_t view, uint64_t start, uint64_t length,
                                     uint32_t selected, QueryCaches& caches) {
    if (length == 0 || (reachable_[view] & selected) == 0) return std::nullopt;
    RangeKey key{view, start, length};
    if (caches.no_mask.contains(key)) return std::nullopt;

    const ViewDef& def = model_.views[view];
    for (size_t item_index : items_by_view_[view]) {
      if ((selected & (1u << item_index)) == 0) continue;
      const Interval& interval = model_.items[item_index].interval;
      uint64_t lo = std::max(start, interval.start);
      uint64_t hi = std::min(start + length, interval.end());
      if (lo < hi) return lo;
    }

    std::optional<uint64_t> result;
    if (def.kind == ViewKind::Slice) {
      auto p = findMasked(def.source, def.source_offset + start, length, selected,
                          caches);
      if (p) result = *p - def.source_offset;
    } else if (def.kind == ViewKind::Reverse) {
      auto p = findMasked(def.source, def.length - (start + length), length,
                          selected, caches);
      if (p) result = def.length - 1 - *p;
    } else if (def.kind == ViewKind::Concat) {
      uint64_t end = start + length;
      size_t part = static_cast<size_t>(
          std::upper_bound(def.prefix.begin(), def.prefix.end(), start) -
          def.prefix.begin());
      if (part > 0) --part;
      while (part < def.parts.size() && def.prefix[part] < end) {
        if ((reachable_[def.parts[part]] & selected) != 0) {
          uint64_t part_start = def.prefix[part];
          uint64_t part_end = def.prefix[part + 1];
          uint64_t lo = std::max(start, part_start);
          uint64_t hi = std::min(end, part_end);
          if (lo < hi) {
            auto p = findMasked(def.parts[part], lo - part_start, hi - lo,
                                selected, caches);
            if (p) {
              result = part_start + *p;
              break;
            }
          }
        }
        ++part;
      }
    }
    if (!result) caches.no_mask.insert(key);
    return result;
  }

  bool itemCoversPosition(size_t item_index, size_t export_index,
                          uint64_t position) const {
    const EditItem& item = model_.items[item_index];
    if (item.interval.empty()) return false;
    size_t view = model_.exports[export_index].view;
    uint64_t p = position;
    while (true) {
      if (view == item.view && p >= item.interval.start &&
          p < item.interval.end()) {
        return true;
      }
      const ViewDef& def = model_.views[view];
      if (def.kind == ViewKind::PageSlice) return false;
      if (def.kind == ViewKind::Slice) {
        p += def.source_offset;
        view = def.source;
      } else if (def.kind == ViewKind::Reverse) {
        p = def.length - 1 - p;
        view = def.source;
      } else {
        size_t part = static_cast<size_t>(
            std::upper_bound(def.prefix.begin(), def.prefix.end(), p) -
            def.prefix.begin());
        if (part > 0) --part;
        if (part >= def.parts.size()) return false;
        p -= def.prefix[part];
        view = def.parts[part];
      }
    }
  }

  bool baseAt(size_t view, uint64_t position, BaseId* out) const {
    uint64_t p = position;
    while (true) {
      const ViewDef& def = model_.views[view];
      if (def.kind == ViewKind::PageSlice) {
        out->page = def.page;
        out->offset = def.page_offset + p;
        return true;
      }
      if (def.kind == ViewKind::Slice) {
        p += def.source_offset;
        view = def.source;
      } else if (def.kind == ViewKind::Reverse) {
        p = def.length - 1 - p;
        view = def.source;
      } else {
        size_t part = static_cast<size_t>(
            std::upper_bound(def.prefix.begin(), def.prefix.end(), p) -
            def.prefix.begin());
        if (part > 0) --part;
        if (part >= def.parts.size()) return false;
        p -= def.prefix[part];
        view = def.parts[part];
      }
    }
  }

  std::optional<Conflict> findSecretKeepOverlap() {
    for (size_t export_index = 0; export_index < model_.exports.size();
         ++export_index) {
      const ExportDef& export_def = model_.exports[export_index];
      for (const Interval& keep : export_def.keep) {
        auto position =
            findSecretPosition(export_def.view, keep.start, keep.length);
        if (!position) continue;
        BaseId base;
        if (!baseAt(export_def.view, *position, &base)) continue;
        Conflict conflict;
        conflict.kind = "secret_keep_overlap";
        conflict.detail =
            "a keep position maps to a byte identity declared secret; it cannot "
            "be both preserved and masked";
        conflict.export_id = export_def.id;
        conflict.position = *position;
        conflict.page_id = model_.pages[base.page].id;
        conflict.page_offset = base.offset;
        return conflict;
      }
    }
    return std::nullopt;
  }

  void filterItemsAgainstKeep() {
    for (size_t item_index = 0; item_index < model_.items.size(); ++item_index) {
      uint32_t bit = (1u << item_index);
      QueryCaches caches;
      for (size_t export_index = 0; export_index < model_.exports.size();
           ++export_index) {
        const ExportDef& export_def = model_.exports[export_index];
        bool hit = false;
        uint64_t hit_position = 0;
        for (const Interval& keep : export_def.keep) {
          auto p = findMasked(export_def.view, keep.start, keep.length, bit, caches);
          if (p) {
            hit = true;
            hit_position = *p;
            break;
          }
        }
        if (hit) {
          allowed_[item_index] = false;
          Conflict conflict;
          conflict.kind = "item_masks_keep";
          conflict.detail = "edit item is forbidden because it masks a keep position";
          conflict.export_id = export_def.id;
          conflict.position = hit_position;
          conflict.blocked_items.push_back(model_.items[item_index].id);
          item_conflicts_.push_back(std::move(conflict));
          break;
        }
      }
    }
  }

  void recordCutConflict(const Witness& witness, uint32_t banned) {
    auto key = std::make_pair(witness.export_index, witness.position);
    if (!cut_keys_.insert(key).second) return;
    if (conflicts_.size() >= 64) return;
    Conflict conflict;
    conflict.kind = "uncoverable_secret";
    conflict.detail =
        "a secret occurrence is not covered by any edit item that remains "
        "selectable";
    const ExportDef& export_def = model_.exports[witness.export_index];
    conflict.export_id = export_def.id;
    conflict.position = witness.position;
    BaseId base;
    if (baseAt(export_def.view, witness.position, &base)) {
      conflict.page_id = model_.pages[base.page].id;
      conflict.page_offset = base.offset;
    }
    for (size_t i = 0; i < model_.items.size(); ++i) {
      if (!itemCoversPosition(i, witness.export_index, witness.position)) continue;
      conflict.blocked_items.push_back(model_.items[i].id);
      if ((banned & (1u << i)) || !allowed_[i]) {
        // Blocked by keep; the id is already recorded.
      }
    }
    conflicts_.push_back(std::move(conflict));
  }

  void appendItemConflicts(SolveResult& result) const {
    for (const Conflict& conflict : item_conflicts_) {
      if (result.conflicts.size() >= 64) break;
      result.conflicts.push_back(conflict);
    }
  }

  uint64_t minAdditional(uint32_t selected, MinContext& ctx) {
    if (budget_exceeded_) return kInf;
    auto memo_it = ctx.memo.find(selected);
    if (memo_it != ctx.memo.end()) return memo_it->second;

    ++nodes_;
    if (nodes_ > budget_) {
      budget_exceeded_ = true;
      return kInf;
    }

    auto witness = findUncovered(selected);
    if (!witness) {
      ctx.memo.emplace(selected, 0);
      return 0;
    }

    std::vector<size_t> candidates;
    for (size_t i = 0; i < model_.items.size(); ++i) {
      uint32_t bit = (1u << i);
      if ((selected & bit) || (ctx.banned & bit) || !allowed_[i]) continue;
      if (itemCoversPosition(i, witness->export_index, witness->position)) {
        candidates.push_back(i);
      }
    }
    if (candidates.empty()) {
      recordCutConflict(*witness, ctx.banned);
      ctx.memo.emplace(selected, kInf);
      return kInf;
    }

    std::sort(candidates.begin(), candidates.end(), [&](size_t a, size_t b) {
      if (model_.items[a].cost != model_.items[b].cost) {
        return model_.items[a].cost < model_.items[b].cost;
      }
      return model_.items[a].id < model_.items[b].id;
    });

    uint64_t best = kInf;
    for (size_t candidate : candidates) {
      uint32_t bit = (1u << candidate);
      uint64_t rest = minAdditional(selected | bit, ctx);
      if (budget_exceeded_) return kInf;
      if (rest == kInf) continue;
      uint64_t item_cost = model_.items[candidate].cost;
      if (item_cost > kInf - rest) continue;
      best = std::min(best, item_cost + rest);
    }
    ctx.memo.emplace(selected, best);
    return best;
  }

  const Model& model_;
  SecretSet secret_;
  uint64_t budget_ = 0;
  uint64_t nodes_ = 0;
  bool budget_exceeded_ = false;
  std::vector<std::vector<size_t>> items_by_view_;
  std::vector<uint32_t> reachable_;
  std::vector<bool> allowed_;
  std::unordered_map<RangeKey, bool, RangeKeyHash> touches_cache_;
  std::vector<Conflict> conflicts_;
  std::vector<Conflict> item_conflicts_;
  std::set<std::pair<size_t, uint64_t>> cut_keys_;
};

}  // namespace

std::string solveStatusName(SolveStatus status) {
  switch (status) {
    case SolveStatus::Optimal: return "optimal";
    case SolveStatus::Unsat: return "unsat";
    case SolveStatus::Undetermined: return "undetermined";
  }
  return "undetermined";
}

Json Conflict::toJson() const {
  Json::Object object;
  object.emplace("kind", Json(kind));
  object.emplace("detail", Json(detail));
  if (!export_id.empty()) object.emplace("export", Json(export_id));
  object.emplace("position", Json(position));
  if (!page_id.empty()) object.emplace("page", Json(page_id));
  object.emplace("page_offset", Json(page_offset));
  Json::Array blocked;
  for (int64_t id : blocked_items) blocked.emplace_back(id);
  object.emplace("covering_items", Json(std::move(blocked)));
  return Json(std::move(object));
}

SolveResult solve(const Model& model, SolveOptions options) {
  SolverImpl solver(model, options.node_budget);
  return solver.run();
}

namespace {

std::optional<uint64_t> readPlanU64(const Json& plan, std::string_view key) {
  const Json* value = plan.find(key);
  if (!value) return std::nullopt;
  try {
    return value->as_u64();
  } catch (...) {
    return std::nullopt;
  }
}

}  // namespace

ValidationResult validatePlan(const Model& model, const Json& plan,
                              bool check_optimality, SolveOptions options) {
  ValidationResult result;
  if (!plan.is_object()) {
    result.errors.push_back("plan must be a JSON object");
    result.message = "invalid plan";
    return result;
  }

  std::string format;
  if (const Json* value = plan.find("format")) {
    if (!value->is_string()) {
      result.errors.push_back("plan.format must be a string");
    } else {
      format = value->as_string();
    }
  }
  if (!format.empty() && format != "gsb.redaction.plan/v1") {
    result.errors.push_back("unsupported plan format '" + format + "'");
  }

  std::string digest;
  if (const Json* value = plan.find("input_sha256")) {
    if (value->is_string()) digest = value->as_string();
  } else if (const Json* value = plan.find("input_digest")) {
    if (value->is_string()) digest = value->as_string();
  }
  if (digest.empty()) {
    result.errors.push_back("plan is missing input_sha256");
  } else if (digest != model.sha256) {
    result.errors.push_back("input_sha256 does not match the supplied input");
  }

  std::string status;
  if (const Json* value = plan.find("status")) {
    if (value->is_string()) status = value->as_string();
  }
  if (status != "optimal" && status != "unsat" && status != "undetermined") {
    result.errors.push_back("plan.status must be optimal, unsat or undetermined");
  }

  const Json* selected_json = plan.find("selected");
  if (!selected_json) selected_json = plan.find("selected_items");
  std::vector<int64_t> selected_ids;
  if (selected_json) {
    if (!selected_json->is_array()) {
      result.errors.push_back("plan.selected must be an array");
    } else {
      for (const Json& value : selected_json->as_array()) {
        try {
          selected_ids.push_back(value.as_i64());
        } catch (...) {
          result.errors.push_back("plan.selected contains a non-integer");
        }
      }
    }
  }

  std::unordered_map<int64_t, size_t> by_id;
  for (size_t i = 0; i < model.items.size(); ++i) by_id[model.items[i].id] = i;
  uint32_t mask = 0;
  uint64_t cost = 0;
  std::set<int64_t> seen;
  for (int64_t id : selected_ids) {
    auto it = by_id.find(id);
    if (it == by_id.end()) {
      result.errors.push_back("plan selects unknown edit item " + std::to_string(id));
      continue;
    }
    if (!seen.insert(id).second) {
      result.errors.push_back("plan selects edit item " + std::to_string(id) +
                              " more than once");
      continue;
    }
    mask |= (1u << it->second);
    uint64_t item_cost = model.items[it->second].cost;
    if (item_cost > std::numeric_limits<uint64_t>::max() - cost) {
      result.errors.push_back("plan total cost overflows uint64");
    } else {
      cost += item_cost;
    }
  }
  for (size_t i = 1; i < selected_ids.size(); ++i) {
    if (selected_ids[i] <= selected_ids[i - 1]) {
      result.errors.push_back(
          "plan.selected must be strictly increasing (sorted item ids)");
      break;
    }
  }

  if (auto claimed = readPlanU64(plan, "total_cost")) {
    if (*claimed != cost) {
      result.errors.push_back("plan.total_cost does not equal the selected item cost");
    }
  } else if (status == "optimal") {
    result.errors.push_back("optimal plan is missing total_cost");
  }

  // A concrete selected set can be checked independently of its claimed
  // status.  Empty selections for unsat/undetermined plans are not required to
  // cover anything, but any non-empty selection must be safe.
  if (!selected_ids.empty() || status == "optimal") {
    SolverImpl checker(model, std::max<uint64_t>(options.node_budget, 1));
    for (size_t export_index = 0; export_index < model.exports.size();
         ++export_index) {
      const ExportDef& export_def = model.exports[export_index];
      for (const Interval& keep : export_def.keep) {
        auto p = checker.checkMasked(export_def.view, keep.start, keep.length, mask);
        if (p) {
          result.errors.push_back("plan masks keep position " +
                                  std::to_string(*p) + " in export '" +
                                  export_def.id + "'");
          break;
        }
      }
    }
    auto witness = checker.checkUncovered(mask);
    if (witness) {
      result.errors.push_back("plan leaves a secret occurrence unmasked at " +
                              std::to_string(witness->second) +
                              " in export '" +
                              model.exports[witness->first].id + "'");
    }
  }

  if (check_optimality && status == "optimal") {
    SolveOptions verification_options;
    verification_options.node_budget = options.node_budget;
    result.recomputed = solve(model, verification_options);
    result.optimality_checked = true;
    if (result.recomputed.status == SolveStatus::Optimal) {
      result.optimality_verified = true;
      if (result.recomputed.total_cost != cost) {
        result.errors.push_back("plan is not optimal: recomputed minimum cost is " +
                                std::to_string(result.recomputed.total_cost));
      } else if (result.recomputed.selected != selected_ids) {
        result.errors.push_back(
            "plan is not the lexicographically smallest minimum-cost selection");
      }
    } else if (result.recomputed.status == SolveStatus::Unsat) {
      result.errors.push_back("plan claims optimality but the input is unsatisfiable");
    } else {
      result.warnings.push_back(
          "optimality could not be verified because the search budget was "
          "exhausted");
    }
  }

  result.valid = result.errors.empty();
  result.message = result.valid ? "valid" : "invalid plan";
  return result;
}

}  // namespace gsb
