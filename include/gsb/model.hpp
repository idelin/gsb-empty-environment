#pragma once

#include <cstdint>
#include <map>
#include <optional>
#include <string>
#include <string_view>
#include <unordered_map>
#include <vector>

#include "gsb/json.hpp"

namespace gsb {

constexpr uint64_t kMaxExpandedLength = 1ull << 48;
constexpr size_t kMaxEditItems = 22;

struct Interval {
  uint64_t start = 0;
  uint64_t length = 0;

  uint64_t end() const { return start + length; }
  bool empty() const { return length == 0; }
};

struct Page {
  std::string id;
  uint64_t size = 0;
  std::vector<uint8_t> data;
  bool has_data = false;
};

enum class ViewKind { PageSlice, Concat, Slice, Reverse };

struct ViewDef {
  std::string id;
  ViewKind kind = ViewKind::PageSlice;
  uint64_t length = 0;

  // PageSlice.
  size_t page = 0;
  uint64_t page_offset = 0;

  // Slice and Reverse.
  size_t source = 0;
  uint64_t source_offset = 0;

  // Concat.  prefix[i] is the start offset of parts[i]; prefix.size() ==
  // parts.size() + 1, with prefix.back() == length.
  std::vector<size_t> parts;
  std::vector<uint64_t> prefix;
};

struct ExportDef {
  std::string id;
  size_t view = 0;
  std::vector<Interval> keep;
  std::vector<Interval> secret;
};

struct EditItem {
  int64_t id = 0;
  size_t view = 0;
  Interval interval;
  uint64_t cost = 0;
};

struct SolverConfig {
  uint64_t node_budget = 2000000;
  uint64_t max_inspect = 4096;
};

struct Model {
  std::string format;
  std::vector<Page> pages;
  std::vector<ViewDef> views;
  std::vector<ExportDef> exports;
  std::vector<EditItem> items;  // sorted by EditItem::id
  SolverConfig config;
  std::string raw_text;
  std::string sha256;

  const Page* findPage(std::string_view id) const;
  const ViewDef* findView(std::string_view id) const;
  const ExportDef* findExport(std::string_view id) const;
};

struct LoadResult {
  std::optional<Model> model;
  std::vector<std::string> errors;
  bool ok() const { return model.has_value() && errors.empty(); }
};

LoadResult loadModel(std::string text);

struct PageRange {
  uint64_t start = 0;
  uint64_t end = 0;
};

class SecretSet {
public:
  void add(size_t page, uint64_t start, uint64_t end);
  void finish();

  bool empty() const { return empty_; }
  bool contains(size_t page, uint64_t offset) const;
  bool intersects(size_t page, uint64_t start, uint64_t end) const;
  const std::vector<PageRange>* ranges(size_t page) const;

private:
  std::unordered_map<size_t, std::vector<PageRange>> ranges_;
  bool empty_ = true;
  bool finished_ = false;
};

SecretSet computeSecretSet(const Model& model);

std::vector<Interval> normalizeIntervals(std::vector<Interval> intervals);
bool intervalsOverlap(const Interval& a, const Interval& b);

Json intervalToJson(const Interval& interval);
std::optional<Interval> parseIntervalJson(const Json& value, std::string* error);

}  // namespace gsb
