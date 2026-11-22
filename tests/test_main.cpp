#include <algorithm>
#include <cstdint>
#include <cstdlib>
#include <iostream>
#include <random>
#include <set>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

#include "gsb/evidence.hpp"
#include "gsb/json.hpp"
#include "gsb/model.hpp"
#include "gsb/sha256.hpp"
#include "gsb/solver.hpp"

namespace {

int g_failures = 0;

void fail(const std::string& message, const char* file, int line) {
  std::cerr << file << ":" << line << ": " << message << "\n";
  ++g_failures;
}

#define CHECK(condition)                                                       \
  do {                                                                         \
    if (!(condition)) {                                                        \
      fail("CHECK failed: " #condition, __FILE__, __LINE__);                   \
    }                                                                          \
  } while (0)

#define CHECK_EQ(actual, expected)                                             \
  do {                                                                         \
    const auto& a_ = (actual);                                                 \
    const auto& e_ = (expected);                                               \
    if (!(a_ == e_)) {                                                         \
      std::ostringstream oss_;                                                 \
      oss_ << "CHECK_EQ failed: " #actual " != " #expected;                    \
      fail(oss_.str(), __FILE__, __LINE__);                                    \
    }                                                                          \
  } while (0)

struct TestBaseId {
  size_t page = 0;
  uint64_t offset = 0;
};

// Independent byte-level expansion used only by the tests.
std::vector<std::vector<TestBaseId>> expandAll(const gsb::Model& model) {
  std::vector<std::vector<TestBaseId>> expanded(model.views.size());
  for (size_t i = 0; i < model.views.size(); ++i) {
    const gsb::ViewDef& def = model.views[i];
    auto& out = expanded[i];
    switch (def.kind) {
      case gsb::ViewKind::PageSlice:
        out.reserve(static_cast<size_t>(def.length));
        for (uint64_t j = 0; j < def.length; ++j) {
          out.push_back(TestBaseId{def.page, def.page_offset + j});
        }
        break;
      case gsb::ViewKind::Slice: {
        const auto& source = expanded[def.source];
        out.reserve(static_cast<size_t>(def.length));
        for (uint64_t j = 0; j < def.length; ++j) {
          out.push_back(source[def.source_offset + j]);
        }
        break;
      }
      case gsb::ViewKind::Reverse: {
        const auto& source = expanded[def.source];
        out.reserve(source.size());
        for (auto it = source.rbegin(); it != source.rend(); ++it) {
          out.push_back(*it);
        }
        break;
      }
      case gsb::ViewKind::Concat:
        for (size_t part : def.parts) {
          const auto& source = expanded[part];
          out.insert(out.end(), source.begin(), source.end());
        }
        break;
    }
  }
  return expanded;
}

std::string expandString(const gsb::Model& model, size_t view) {
  auto expanded = expandAll(model);
  std::string out;
  for (const TestBaseId& base : expanded[view]) {
    out.push_back(static_cast<char>(model.pages[base.page].data[base.offset]));
  }
  return out;
}

std::vector<std::vector<uint8_t>> expandMasks(const gsb::Model& model,
                                              uint32_t selected) {
  std::vector<std::vector<uint8_t>> masks(model.views.size());
  for (size_t i = 0; i < model.views.size(); ++i) {
    const gsb::ViewDef& def = model.views[i];
    auto& mask = masks[i];
    mask.clear();
    switch (def.kind) {
      case gsb::ViewKind::PageSlice:
        mask.assign(static_cast<size_t>(def.length), 0);
        break;
      case gsb::ViewKind::Slice: {
        const auto& source = masks[def.source];
        mask.resize(static_cast<size_t>(def.length), 0);
        for (uint64_t j = 0; j < def.length; ++j) {
          mask[j] = source[def.source_offset + j];
        }
        break;
      }
      case gsb::ViewKind::Reverse: {
        const auto& source = masks[def.source];
        mask.resize(static_cast<size_t>(def.length), 0);
        for (uint64_t j = 0; j < def.length; ++j) {
          mask[j] = source[def.length - 1 - j];
        }
        break;
      }
      case gsb::ViewKind::Concat:
        for (size_t part : def.parts) {
          const auto& source = masks[part];
          mask.insert(mask.end(), source.begin(), source.end());
        }
        break;
    }
    for (size_t item_index = 0; item_index < model.items.size(); ++item_index) {
      if ((selected & (1u << item_index)) == 0) continue;
      const gsb::EditItem& item = model.items[item_index];
      if (item.view != i) continue;
      uint64_t lo = std::min<uint64_t>(item.interval.start, def.length);
      uint64_t hi = std::min<uint64_t>(item.interval.end(), def.length);
      for (uint64_t j = lo; j < hi; ++j) mask[j] = 1;
    }
  }
  return masks;
}

struct BruteResult {
  bool found = false;
  uint64_t cost = 0;
  std::vector<int64_t> ids;
};

bool idsLess(const std::vector<int64_t>& a, const std::vector<int64_t>& b) {
  return std::lexicographical_compare(a.begin(), a.end(), b.begin(), b.end());
}

BruteResult bruteForce(const gsb::Model& model) {
  auto expanded = expandAll(model);
  std::set<std::pair<size_t, uint64_t>> secret;
  for (const gsb::ExportDef& export_def : model.exports) {
    const auto& bytes = expanded[export_def.view];
    for (const gsb::Interval& interval : export_def.secret) {
      for (uint64_t p = interval.start; p < interval.end(); ++p) {
        secret.insert({bytes[p].page, bytes[p].offset});
      }
    }
  }

  BruteResult best;
  const size_t item_count = model.items.size();
  const uint64_t subset_count = 1ull << item_count;
  for (uint64_t subset = 0; subset < subset_count; ++subset) {
    uint64_t cost = 0;
    std::vector<int64_t> ids;
    for (size_t i = 0; i < item_count; ++i) {
      if (subset & (1ull << i)) {
        cost += model.items[i].cost;
        ids.push_back(model.items[i].id);
      }
    }
    if (best.found &&
        (cost > best.cost || (cost == best.cost && !idsLess(ids, best.ids)))) {
      continue;
    }

    auto masks = expandMasks(model, static_cast<uint32_t>(subset));
    bool valid = true;
    for (const gsb::ExportDef& export_def : model.exports) {
      const auto& bytes = expanded[export_def.view];
      const auto& mask = masks[export_def.view];
      for (uint64_t p = 0; p < bytes.size() && valid; ++p) {
        bool is_secret = secret.contains({bytes[p].page, bytes[p].offset});
        bool is_kept = false;
        for (const gsb::Interval& keep : export_def.keep) {
          if (p >= keep.start && p < keep.end()) {
            is_kept = true;
            break;
          }
        }
        if (is_secret && !mask[p]) valid = false;
        if (is_kept && mask[p]) valid = false;
      }
      if (!valid) break;
    }
    if (!valid) continue;
    if (!best.found || cost < best.cost ||
        (cost == best.cost && idsLess(ids, best.ids))) {
      best.found = true;
      best.cost = cost;
      best.ids = std::move(ids);
    }
  }
  return best;
}

gsb::Model loadModelOrDie(const std::string& text) {
  gsb::LoadResult loaded = gsb::loadModel(text);
  if (!loaded.ok()) {
    std::cerr << "failed to load test model:\n";
    for (const std::string& error : loaded.errors) std::cerr << "  " << error << "\n";
    std::abort();
  }
  return std::move(*loaded.model);
}

void compareSolveToBrute(const gsb::Model& model, const std::string& label) {
  BruteResult expected = bruteForce(model);
  gsb::SolveOptions options;
  options.node_budget = 10'000'000;
  gsb::SolveResult actual = gsb::solve(model, options);
  if (!expected.found) {
    if (actual.status != gsb::SolveStatus::Unsat) {
      fail(label + ": expected unsat, got " + gsb::solveStatusName(actual.status),
           __FILE__, __LINE__);
    }
    return;
  }
  if (actual.status != gsb::SolveStatus::Optimal) {
    fail(label + ": expected optimal, got " +
             gsb::solveStatusName(actual.status),
         __FILE__, __LINE__);
    return;
  }
  if (actual.total_cost != expected.cost || actual.selected != expected.ids) {
    std::ostringstream oss;
    oss << label << ": solver returned cost " << actual.total_cost << " ids [";
    for (size_t i = 0; i < actual.selected.size(); ++i) {
      if (i) oss << ",";
      oss << actual.selected[i];
    }
    oss << "], brute returned cost " << expected.cost << " ids [";
    for (size_t i = 0; i < expected.ids.size(); ++i) {
      if (i) oss << ",";
      oss << expected.ids[i];
    }
    oss << "]";
    fail(oss.str(), __FILE__, __LINE__);
  }
}

gsb::Page makePage(std::string id, std::string bytes) {
  gsb::Page page;
  page.id = std::move(id);
  page.data.assign(bytes.begin(), bytes.end());
  page.size = bytes.size();
  page.has_data = true;
  return page;
}

size_t addPageSlice(gsb::Model& model, size_t page, uint64_t start,
                    uint64_t length, std::string id) {
  gsb::ViewDef def;
  def.id = std::move(id);
  def.kind = gsb::ViewKind::PageSlice;
  def.page = page;
  def.page_offset = start;
  def.length = length;
  model.views.push_back(std::move(def));
  return model.views.size() - 1;
}

size_t addSlice(gsb::Model& model, size_t source, uint64_t start, uint64_t length,
                std::string id) {
  gsb::ViewDef def;
  def.id = std::move(id);
  def.kind = gsb::ViewKind::Slice;
  def.source = source;
  def.source_offset = start;
  def.length = length;
  model.views.push_back(std::move(def));
  return model.views.size() - 1;
}

size_t addReverse(gsb::Model& model, size_t source, std::string id) {
  gsb::ViewDef def;
  def.id = std::move(id);
  def.kind = gsb::ViewKind::Reverse;
  def.source = source;
  def.length = model.views[source].length;
  model.views.push_back(std::move(def));
  return model.views.size() - 1;
}

size_t addConcat(gsb::Model& model, std::vector<size_t> parts, std::string id) {
  gsb::ViewDef def;
  def.id = std::move(id);
  def.kind = gsb::ViewKind::Concat;
  def.parts = std::move(parts);
  def.prefix.push_back(0);
  for (size_t part : def.parts) {
    def.prefix.push_back(def.prefix.back() + model.views[part].length);
  }
  def.length = def.prefix.back();
  model.views.push_back(std::move(def));
  return model.views.size() - 1;
}

void addExport(gsb::Model& model, std::string id, size_t view,
               std::vector<gsb::Interval> keep,
               std::vector<gsb::Interval> secret) {
  gsb::ExportDef def;
  def.id = std::move(id);
  def.view = view;
  def.keep = std::move(keep);
  def.secret = std::move(secret);
  model.exports.push_back(std::move(def));
}

void addItem(gsb::Model& model, int64_t id, size_t view, uint64_t start,
             uint64_t length, uint64_t cost) {
  gsb::EditItem item;
  item.id = id;
  item.view = view;
  item.interval = gsb::Interval{start, length};
  item.cost = cost;
  model.items.push_back(item);
}

void testByteExpansion() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCDEF"));
  size_t a = addPageSlice(model, 0, 0, 6, "a");
  CHECK_EQ(expandString(model, a), std::string("ABCDEF"));
  size_t c = addConcat(model, {a, a}, "c");
  CHECK_EQ(expandString(model, c), std::string("ABCDEFABCDEF"));
  size_t s = addSlice(model, c, 2, 5, "s");
  CHECK_EQ(expandString(model, s), std::string("CDEFA"));
  size_t r = addReverse(model, s, "r");
  CHECK_EQ(expandString(model, r), std::string("AFEDC"));
  size_t s2 = addSlice(model, r, 1, 3, "s2");
  CHECK_EQ(expandString(model, s2), std::string("FED"));
  size_t rr = addReverse(model, s2, "rr");
  CHECK_EQ(expandString(model, rr), std::string("DEF"));
}

void testSha256() {
  CHECK_EQ(gsb::sha256_hex(""),
           std::string("e3b0c44298fc1c149afbf4c8996fb924"
                       "27ae41e4649b934ca495991b7852b855"));
  CHECK_EQ(gsb::sha256_hex("abc"),
           std::string("ba7816bf8f01cfea414140de5dae2223"
                       "b00361a396177a9cb410ff61f20015ad"));
}

void testDuplicateSubtreeLastByte() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  size_t dup = addConcat(model, {a, a}, "dup");
  addExport(model, "e", dup, {}, {{7, 1}});
  addItem(model, 0, a, 3, 1, 5);
  addItem(model, 1, dup, 7, 1, 1);
  addItem(model, 2, dup, 3, 1, 4);
  compareSolveToBrute(model, "duplicate subtree last byte");

  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{5});
  CHECK(result.selected == std::vector<int64_t>({0}));
}

void testReverseThenSlice() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCDEF"));
  size_t a = addPageSlice(model, 0, 0, 6, "a");
  size_t rev = addReverse(model, a, "rev");
  size_t s = addSlice(model, rev, 1, 3, "s");
  CHECK_EQ(expandString(model, s), std::string("EDC"));
  addExport(model, "e", s, {}, {{0, 1}});
  addItem(model, 0, rev, 1, 1, 2);
  addItem(model, 1, s, 0, 1, 3);
  addItem(model, 2, a, 4, 1, 4);
  addItem(model, 3, a, 1, 1, 1);
  compareSolveToBrute(model, "reverse plus slice");
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{2});
  CHECK(result.selected == std::vector<int64_t>({0}));
}

void testSecretKeepOverlap() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  size_t rev = addReverse(model, a, "rev");
  addExport(model, "e1", a, {}, {{0, 1}});
  addExport(model, "e2", rev, {{3, 1}}, {});
  addItem(model, 0, a, 0, 1, 1);
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Unsat);
  CHECK(!result.conflicts.empty());
  if (!result.conflicts.empty()) {
    CHECK_EQ(result.conflicts[0].kind, std::string("secret_keep_overlap"));
    CHECK_EQ(result.conflicts[0].export_id, std::string("e2"));
  }
}

void testCoverageCombination() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  size_t dup = addConcat(model, {a, a}, "dup");
  // The secret interval [3,5) covers D at 3 and A at 4.  Their aliases are
  // D at 7 and A at 0, so the union of item coverages is not the top-level
  // interval [3,5).
  addExport(model, "e", dup, {}, {{3, 2}});
  addItem(model, 0, a, 3, 1, 1);
  addItem(model, 1, a, 0, 1, 1);
  addItem(model, 2, dup, 0, 8, 10);
  compareSolveToBrute(model, "coverage combination");
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{2});
  CHECK(result.selected == std::vector<int64_t>({0, 1}));
}

void testCrossExportAliases() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  size_t rev = addReverse(model, a, "rev");
  size_t first = addSlice(model, a, 0, 1, "first");
  addExport(model, "e1", first, {}, {{0, 1}});  // A is secret.
  addExport(model, "e2", rev, {}, {});          // rev[3] is the alias A.
  addItem(model, 0, a, 0, 1, 3);                // masks both aliases.
  addItem(model, 1, rev, 3, 1, 1);              // masks only the e2 alias.
  addItem(model, 2, first, 0, 1, 1);            // masks only e1.
  compareSolveToBrute(model, "cross export aliases");
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{2});
  CHECK(result.selected == std::vector<int64_t>({1, 2}));
}

void testKeepForbidsItem() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  addExport(model, "e", a, {{0, 1}}, {{1, 1}});
  addItem(model, 0, a, 0, 2, 1);  // would mask keep byte A
  addItem(model, 1, a, 1, 1, 7);
  compareSolveToBrute(model, "keep forbids item");
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{7});
  CHECK(result.selected == std::vector<int64_t>({1}));
}

void testUnsatisfiableCover() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  addExport(model, "e", a, {{0, 1}}, {{1, 1}});
  addItem(model, 0, a, 0, 2, 1);  // forbidden by keep
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Unsat);
  CHECK(!result.conflicts.empty());
}

void testLexicographicTieBreak() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCD"));
  size_t a = addPageSlice(model, 0, 0, 4, "a");
  addExport(model, "e", a, {}, {{1, 1}});
  addItem(model, 0, a, 1, 1, 2);
  addItem(model, 1, a, 1, 1, 2);
  addItem(model, 2, a, 1, 1, 2);
  compareSolveToBrute(model, "lexicographic tie");
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK(result.selected == std::vector<int64_t>({0}));

  // A shorter prefix is lexicographically smaller than a zero-cost extension.
  gsb::Model prefix;
  prefix.pages.push_back(makePage("p", "ABCD"));
  size_t a2 = addPageSlice(prefix, 0, 0, 4, "a");
  addExport(prefix, "e", a2, {}, {{1, 1}});
  addItem(prefix, 0, a2, 1, 1, 2);
  addItem(prefix, 1, a2, 0, 1, 0);  // free, but irrelevant; not auto-selected
  compareSolveToBrute(prefix, "lexicographic prefix");
  gsb::SolveResult result2 = gsb::solve(prefix, {1'000'000});
  CHECK(result2.selected == std::vector<int64_t>({0}));

  // A smaller-id zero-cost irrelevant item still improves the lexicographic
  // list: [0,1] < [1] for sorted lists.
  gsb::Model free_first;
  free_first.pages.push_back(makePage("p", "ABCD"));
  size_t a3 = addPageSlice(free_first, 0, 0, 4, "a");
  addExport(free_first, "e", a3, {}, {{1, 1}});
  addItem(free_first, 0, a3, 0, 1, 0);
  addItem(free_first, 1, a3, 1, 1, 2);
  compareSolveToBrute(free_first, "lexicographic free prefix");
  gsb::SolveResult result3 = gsb::solve(free_first, {1'000'000});
  CHECK(result3.selected == std::vector<int64_t>({0, 1}));
}

void testBudgetUndetermined() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "AB"));
  size_t a = addPageSlice(model, 0, 0, 2, "a");
  addExport(model, "e", a, {}, {{1, 1}});
  for (int i = 0; i < 22; ++i) addItem(model, i, a, 1, 1, 1);
  gsb::SolveResult result = gsb::solve(model, {1});
  CHECK_EQ(result.status, gsb::SolveStatus::Undetermined);
}

void testTwentyTwoItems() {
  gsb::Model model;
  model.pages.push_back(makePage("p", "ABCDEFGHIJKLMNOPQRSTUV"));
  size_t a = addPageSlice(model, 0, 0, 22, "a");
  addExport(model, "e", a, {}, {{0, 22}});
  for (int i = 0; i < 22; ++i) {
    addItem(model, i, a, static_cast<uint64_t>(i), 1, 1);
  }
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{22});
  std::vector<int64_t> expected;
  for (int i = 0; i < 22; ++i) expected.push_back(i);
  CHECK(result.selected == expected);
}

void testHugeExpansion() {
  constexpr uint64_t kTwo48 = 1ull << 48;
  gsb::Model model;
  model.pages.push_back(makePage("p", "AB"));
  size_t current = addPageSlice(model, 0, 0, 2, "v0");
  for (int level = 1; level <= 47; ++level) {
    current = addConcat(model, {current, current}, "v" + std::to_string(level));
  }
  CHECK_EQ(model.views[current].length, kTwo48);
  addExport(model, "e", current, {}, {{kTwo48 - 1, 1}});
  addItem(model, 0, 0, 1, 1, 5);                  // v0 B, all aliases.
  addItem(model, 1, current, kTwo48 - 1, 1, 1);   // only the last alias.
  addItem(model, 2, current, 0, kTwo48, 4);       // whole huge view.
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{4});
  CHECK(result.selected == std::vector<int64_t>({2}));
  CHECK(result.nodes_explored < 100000);
  model.sha256 = "huge-test-digest";
  gsb::Json plan = gsb::buildPlanJson(model, result);
  gsb::EvidenceResult evidence =
      gsb::inspectRange(model, plan, "e", kTwo48 - 1, 1, 8);
  CHECK(evidence.ok);
  if (evidence.ok) {
    const std::string dumped = evidence.evidence.dump(0);
    CHECK(dumped.find("\"masked\":true") != std::string::npos);
    CHECK(dumped.find("\"value\"") == std::string::npos);
  }

  // Without the whole-view item, the shared one-byte item is needed.
  gsb::Model child_only;
  child_only.pages.push_back(makePage("q", "AB"));
  size_t c = addPageSlice(child_only, 0, 0, 2, "w0");
  for (int level = 1; level <= 47; ++level) {
    c = addConcat(child_only, {c, c}, "w" + std::to_string(level));
  }
  addExport(child_only, "e", c, {}, {{kTwo48 - 1, 1}});
  addItem(child_only, 0, 0, 1, 1, 5);
  addItem(child_only, 1, c, kTwo48 - 1, 1, 1);
  gsb::SolveResult child_result = gsb::solve(child_only, {1'000'000});
  CHECK_EQ(child_result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(child_result.total_cost, uint64_t{5});
  CHECK(child_result.selected == std::vector<int64_t>({0}));
}

void testJsonForwardReferenceAndEvidence() {
  std::string input = R"JSON({
    "format": "gsb.redaction.input/v1",
    "pages": [{"id": "p", "size": 4, "data_hex": "41424344"}],
    "views": [
      {"id": "s", "op": "slice", "source": "a", "start": 1, "length": 2},
      {"id": "a", "op": "page", "page": "p", "start": 0, "length": 4},
      {"id": "r", "op": "reverse", "source": "s"}
    ],
    "exports": [
      {"id": "e", "view": "r", "keep": [{"start": 0, "length": 1}],
       "secret": [{"start": 1, "length": 1}]}
    ],
    "edits": [
      {"id": 0, "view": "a", "start": 2, "length": 1, "cost": 4},
      {"id": 1, "view": "r", "start": 1, "length": 1, "cost": 2}
    ]
  })JSON";
  gsb::Model model = loadModelOrDie(input);
  CHECK_EQ(expandString(model, 2), std::string("CB"));
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK_EQ(result.total_cost, uint64_t{2});
  CHECK(result.selected == std::vector<int64_t>({1}));

  gsb::Json plan = gsb::buildPlanJson(model, result);
  gsb::ValidationResult validation = gsb::validatePlan(model, plan, true);
  CHECK(validation.valid);
  CHECK(validation.optimality_verified);

  gsb::EvidenceResult evidence =
      gsb::inspectRange(model, plan, "e", 0, 2, 8);
  CHECK(evidence.ok);
  CHECK(evidence.evidence.is_object());
  const std::string dumped = evidence.evidence.dump(0);
  CHECK(dumped.find("\"value\"") == std::string::npos);
  CHECK(dumped.find("\"byte\"") == std::string::npos);
  CHECK(dumped.find("contains_plaintext") != std::string::npos);

  gsb::EvidenceResult too_long = gsb::inspectRange(model, plan, "e", 0, 3, 2);
  CHECK(!too_long.ok);

  // Tampering with cost or selection must be rejected.
  gsb::Json tampered = plan;
  tampered["total_cost"] = gsb::Json(3);
  CHECK(!gsb::validatePlan(model, tampered, false).valid);
  gsb::Json tampered2 = plan;
  tampered2["selected"] = gsb::Json(gsb::Json::Array{});
  CHECK(!gsb::validatePlan(model, tampered2, false).valid);
}

void testSparseEditIds() {
  std::string input = R"JSON({
    "pages": [{"id": "p", "size": 2, "data_hex": "4142"}],
    "views": [{"id": "a", "op": "page", "page": "p", "start": 0, "length": 2}],
    "exports": [{"id": "e", "view": "a", "secret": [{"start": 1, "length": 1}]}],
    "edits": [
      {"id": 9, "view": "a", "start": 1, "length": 1, "cost": 1},
      {"id": 2, "view": "a", "start": 1, "length": 1, "cost": 1},
      {"id": 5, "view": "a", "start": 1, "length": 1, "cost": 1}
    ]
  })JSON";
  gsb::Model model = loadModelOrDie(input);
  gsb::SolveResult result = gsb::solve(model, {1'000'000});
  CHECK_EQ(result.status, gsb::SolveStatus::Optimal);
  CHECK(result.selected == std::vector<int64_t>({2}));
}

gsb::Model randomSmallModel(std::mt19937& rng) {
  auto rand_u64 = [&](uint64_t bound) -> uint64_t {
    return bound == 0
               ? 0
               : std::uniform_int_distribution<uint64_t>(0, bound - 1)(rng);
  };
  gsb::Model model;
  int page_count = 1 + static_cast<int>(rand_u64(2));
  for (int p = 0; p < page_count; ++p) {
    int size = 1 + static_cast<int>(rand_u64(5));
    std::string bytes;
    for (int i = 0; i < size; ++i) {
      bytes.push_back(static_cast<char>('A' + rand_u64(6)));
    }
    model.pages.push_back(makePage("p" + std::to_string(p), bytes));
  }
  int initial = 1 + static_cast<int>(rand_u64(2));
  for (int i = 0; i < initial; ++i) {
    size_t page = rand_u64(model.pages.size());
    uint64_t length = model.pages[page].size;
    addPageSlice(model, page, 0, length, "v" + std::to_string(i));
  }
  int extra = 2 + static_cast<int>(rand_u64(5));
  for (int i = 0; i < extra; ++i) {
    std::string id = "v" + std::to_string(model.views.size());
    size_t source = rand_u64(model.views.size());
    int kind = static_cast<int>(rand_u64(4));
    if (kind == 0) {
      size_t page = rand_u64(model.pages.size());
      uint64_t start = rand_u64(model.pages[page].size);
      uint64_t length = 1 + rand_u64(model.pages[page].size - start);
      addPageSlice(model, page, start, length, id);
    } else if (kind == 1) {
      size_t a = rand_u64(model.views.size());
      size_t b = rand_u64(model.views.size());
      addConcat(model, {a, b}, id);
    } else if (kind == 2) {
      uint64_t source_length = model.views[source].length;
      uint64_t start = rand_u64(source_length + 1);
      uint64_t length = rand_u64(source_length - start + 1);
      addSlice(model, source, start, length, id);
    } else {
      addReverse(model, source, id);
    }
  }
  int exports = 1 + static_cast<int>(rand_u64(2));
  for (int e = 0; e < exports; ++e) {
    size_t view = rand_u64(model.views.size());
    uint64_t length = model.views[view].length;
    std::vector<gsb::Interval> keep;
    std::vector<gsb::Interval> secret;
    int keep_count = static_cast<int>(rand_u64(3));
    int secret_count = static_cast<int>(rand_u64(3));
    for (int k = 0; k < keep_count && length > 0; ++k) {
      uint64_t start = rand_u64(length);
      uint64_t len = 1 + rand_u64(length - start);
      keep.push_back({start, len});
    }
    for (int s = 0; s < secret_count && length > 0; ++s) {
      uint64_t start = rand_u64(length);
      uint64_t len = 1 + rand_u64(length - start);
      secret.push_back({start, len});
    }
    addExport(model, "e" + std::to_string(e), view, std::move(keep),
              std::move(secret));
  }
  int items = static_cast<int>(rand_u64(7));
  for (int i = 0; i < items; ++i) {
    size_t view = rand_u64(model.views.size());
    uint64_t length = model.views[view].length;
    uint64_t start = length == 0 ? 0 : rand_u64(length);
    uint64_t len = length == 0 ? 0 : rand_u64(length - start + 1);
    addItem(model, i, view, start, len, rand_u64(5));
  }
  return model;
}

void testRandomCrossCheck() {
  std::mt19937 rng(0x5eed1234);
  for (int case_index = 0; case_index < 1000; ++case_index) {
    gsb::Model model = randomSmallModel(rng);
    compareSolveToBrute(model, "random case " + std::to_string(case_index));
  }
}

}  // namespace

int main() {
  testSha256();
  testByteExpansion();
  testDuplicateSubtreeLastByte();
  testReverseThenSlice();
  testSecretKeepOverlap();
  testCoverageCombination();
  testCrossExportAliases();
  testKeepForbidsItem();
  testUnsatisfiableCover();
  testLexicographicTieBreak();
  testBudgetUndetermined();
  testTwentyTwoItems();
  testHugeExpansion();
  testJsonForwardReferenceAndEvidence();
  testSparseEditIds();
  testRandomCrossCheck();

  if (g_failures == 0) {
    std::cout << "all tests passed\n";
    return 0;
  }
  std::cerr << g_failures << " test assertion(s) failed\n";
  return 1;
}
