#include "gsb/evidence.hpp"

#include <algorithm>
#include <unordered_map>
#include <vector>

#include "gsb/sha256.hpp"

namespace gsb {

namespace {

std::optional<uint32_t> selectedMask(const Model& model, const Json& plan,
                                     std::vector<int64_t>* selected_ids,
                                     std::string* error) {
  const Json* selected = plan.find("selected");
  if (!selected) selected = plan.find("selected_items");
  if (!selected || !selected->is_array()) {
    *error = "plan.selected must be an array";
    return std::nullopt;
  }
  std::unordered_map<int64_t, size_t> by_id;
  for (size_t i = 0; i < model.items.size(); ++i) by_id[model.items[i].id] = i;
  uint32_t mask = 0;
  for (const Json& value : selected->as_array()) {
    int64_t id = 0;
    try {
      id = value.as_i64();
    } catch (...) {
      *error = "plan.selected contains a non-integer";
      return std::nullopt;
    }
    auto it = by_id.find(id);
    if (it == by_id.end()) {
      *error = "plan selects unknown edit item " + std::to_string(id);
      return std::nullopt;
    }
    mask |= (1u << it->second);
    selected_ids->push_back(id);
  }
  return mask;
}

struct Step {
  std::string view;
  uint64_t offset = 0;
};

struct BaseId {
  size_t page = 0;
  uint64_t offset = 0;
};

}  // namespace

Json buildPlanJson(const Model& model, const SolveResult& result) {
  Json::Object plan;
  plan.emplace("format", Json("gsb.redaction.plan/v1"));
  plan.emplace("input_sha256", Json(model.sha256));
  plan.emplace("digest_alg", Json("sha256"));
  plan.emplace("status", Json(solveStatusName(result.status)));

  Json::Array selected;
  for (int64_t id : result.selected) selected.emplace_back(id);
  plan.emplace("selected", Json(std::move(selected)));
  plan.emplace("total_cost", Json(result.total_cost));

  Json::Array conflicts;
  for (const Conflict& conflict : result.conflicts) {
    conflicts.push_back(conflict.toJson());
  }
  plan.emplace("conflicts", Json(std::move(conflicts)));

  Json::Object solver;
  solver.emplace("nodes_explored", Json(result.nodes_explored));
  solver.emplace("node_budget", Json(result.node_budget));
  plan.emplace("solver", Json(std::move(solver)));

  if (result.has_best_known) {
    Json::Object best;
    Json::Array ids;
    for (int64_t id : result.best_known) ids.emplace_back(id);
    best.emplace("selected", Json(std::move(ids)));
    best.emplace("total_cost", Json(result.best_known_cost));
    plan.emplace("best_known", Json(std::move(best)));
  }

  Json::Object policy;
  policy.emplace("secrecy_domain", Json("base-byte-identity"));
  policy.emplace("masking_domain", Json("view-definition-interval"));
  policy.emplace("budget_exhaustion", Json("undetermined"));
  policy.emplace("tie_break", Json("lexicographic-sorted-id-list"));
  plan.emplace("policy", Json(std::move(policy)));
  return Json(std::move(plan));
}

EvidenceResult inspectRange(const Model& model, const Json& plan,
                            std::string_view export_id, uint64_t start,
                            uint64_t length, uint64_t max_length) {
  EvidenceResult result;

  if (length > max_length) {
    result.error = "requested range exceeds max_inspect (" +
                   std::to_string(max_length) + " bytes)";
    return result;
  }

  const ExportDef* export_def = model.findExport(export_id);
  if (!export_def) {
    result.error = "unknown export '" + std::string(export_id) + "'";
    return result;
  }
  uint64_t view_length = model.views[export_def->view].length;
  if (start > view_length || length > view_length - start) {
    result.error = "requested range is outside the export";
    return result;
  }

  std::string status;
  if (const Json* value = plan.find("status"); value && value->is_string()) {
    status = value->as_string();
  }
  if (status != "optimal") {
    result.error = "evidence can only be read from an optimal plan";
    return result;
  }

  ValidationResult validation = validatePlan(model, plan, false);
  if (!validation.valid) {
    result.error = "plan is not valid: " + validation.message;
    for (const std::string& error : validation.errors) {
      result.error += "; " + error;
    }
    return result;
  }

  std::vector<int64_t> selected_ids;
  std::string mask_error;
  auto mask_opt = selectedMask(model, plan, &selected_ids, &mask_error);
  if (!mask_opt) {
    result.error = mask_error;
    return result;
  }
  uint32_t mask = *mask_opt;

  SecretSet secret = computeSecretSet(model);
  std::vector<std::vector<size_t>> items_by_view(model.views.size());
  for (size_t i = 0; i < model.items.size(); ++i) {
    items_by_view[model.items[i].view].push_back(i);
  }
  Json::Array bytes;
  uint64_t end = start + length;
  for (uint64_t position = start; position < end; ++position) {
    size_t view = export_def->view;
    uint64_t p = position;
    std::vector<Step> path;
    std::vector<int64_t> mask_items;
    BaseId base;
    bool have_base = false;
    while (true) {
      path.push_back(Step{model.views[view].id, p});
      const ViewDef& def = model.views[view];
      for (size_t item_index : items_by_view[view]) {
        if ((mask & (1u << item_index)) == 0) continue;
        const Interval& interval = model.items[item_index].interval;
        if (p >= interval.start && p < interval.end()) {
          mask_items.push_back(model.items[item_index].id);
        }
      }

      if (def.kind == ViewKind::PageSlice) {
        base.page = def.page;
        base.offset = def.page_offset + p;
        have_base = true;
        break;
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
        if (part >= def.parts.size()) {
          result.error = "internal provenance walk failed";
          return result;
        }
        p -= def.prefix[part];
        view = def.parts[part];
      }
    }

    if (!have_base) {
      result.error = "internal base identity walk failed";
      return result;
    }
    bool is_secret = secret.contains(base.page, base.offset);
    bool masked = !mask_items.empty();
    if (is_secret && !masked) {
      result.error =
          "plan leaves a secret occurrence unmasked; refusing to emit evidence";
      return result;
    }

    Json::Object byte;
    byte.emplace("offset", Json(position));
    byte.emplace("masked", Json(masked));
    Json::Array ids;
    for (int64_t id : mask_items) ids.emplace_back(id);
    byte.emplace("mask_items", Json(std::move(ids)));
    byte.emplace("secret", Json(is_secret));
    Json::Object base_json;
    base_json.emplace("page", Json(model.pages[base.page].id));
    base_json.emplace("offset", Json(base.offset));
    byte.emplace("base", Json(std::move(base_json)));
    Json::Array path_json;
    for (const Step& step : path) {
      Json::Object step_json;
      step_json.emplace("view", Json(step.view));
      step_json.emplace("offset", Json(step.offset));
      path_json.emplace_back(std::move(step_json));
    }
    byte.emplace("path", Json(std::move(path_json)));
    bytes.emplace_back(std::move(byte));
  }

  Json::Object evidence;
  evidence.emplace("format", Json("gsb.redaction.evidence/v1"));
  evidence.emplace("input_sha256", Json(model.sha256));
  evidence.emplace("digest_alg", Json("sha256"));
  evidence.emplace("plan_canonical_sha256", Json(sha256_hex(plan.dump(0))));
  evidence.emplace("export", Json(std::string(export_id)));
  evidence.emplace("start", Json(start));
  evidence.emplace("length", Json(length));
  evidence.emplace("max_length", Json(max_length));
  evidence.emplace("contains_plaintext", Json(false));
  evidence.emplace("values_omitted", Json(true));
  evidence.emplace("bytes", Json(std::move(bytes)));
  result.evidence = Json(std::move(evidence));
  result.ok = true;
  return result;
}

}  // namespace gsb
