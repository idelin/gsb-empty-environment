#include "gsb/model.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <limits>
#include <set>
#include <stdexcept>
#include <tuple>
#include <unordered_set>

#include "gsb/sha256.hpp"

namespace gsb {

namespace {

void addError(LoadResult& result, std::string message) {
  result.errors.push_back(std::move(message));
}

std::optional<std::string> readString(const Json& object, std::string_view key,
                                      std::string* error, std::string_view where,
                                      bool required) {
  const Json* value = object.find(key);
  if (!value) {
    if (required && error) {
      *error = std::string(where) + ": missing string field '" + std::string(key) + "'";
    }
    return std::nullopt;
  }
  if (!value->is_string()) {
    if (error) {
      *error = std::string(where) + "." + std::string(key) + ": expected string";
    }
    return std::nullopt;
  }
  return value->as_string();
}

bool validUtf8AsciiId(std::string_view id) {
  if (id.empty()) return false;
  for (char c : id) {
    unsigned char u = static_cast<unsigned char>(c);
    if (u < 0x21 || u > 0x7e) return false;
  }
  return true;
}

int hexNibble(char c) {
  if (c >= '0' && c <= '9') return c - '0';
  if (c >= 'a' && c <= 'f') return c - 'a' + 10;
  if (c >= 'A' && c <= 'F') return c - 'A' + 10;
  return -1;
}

std::optional<std::vector<uint8_t>> decodeHex(std::string_view text, std::string* error) {
  if (text.size() % 2 != 0) {
    if (error) *error = "hex data must have even length";
    return std::nullopt;
  }
  std::vector<uint8_t> out;
  out.reserve(text.size() / 2);
  for (size_t i = 0; i < text.size(); i += 2) {
    int hi = hexNibble(text[i]);
    int lo = hexNibble(text[i + 1]);
    if (hi < 0 || lo < 0) {
      if (error) *error = "invalid hex digit";
      return std::nullopt;
    }
    out.push_back(static_cast<uint8_t>((hi << 4) | lo));
  }
  return out;
}

std::optional<std::vector<uint8_t>> decodeBase64(std::string_view text,
                                                 std::string* error) {
  static constexpr int8_t kDecode[256] = {
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, 62, -1, -1, -1, 63,
      52, 53, 54, 55, 56, 57, 58, 59, 60, 61, -1, -1, -1, -1, -1, -1,
      -1, 0,  1,  2,  3,  4,  5,  6,  7,  8,  9,  10, 11, 12, 13, 14,
      15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, -1, -1, -1, -1, -1,
      -1, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40,
      41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
      -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1,
  };

  std::vector<uint8_t> out;
  uint32_t accumulator = 0;
  int bits = 0;
  int padding = 0;
  for (char c : text) {
    if (c == ' ' || c == '\t' || c == '\r' || c == '\n') continue;
    if (c == '=') {
      ++padding;
      continue;
    }
    if (padding) {
      if (error) *error = "base64 data after padding";
      return std::nullopt;
    }
    int8_t value = kDecode[static_cast<unsigned char>(c)];
    if (value < 0) {
      if (error) *error = "invalid base64 digit";
      return std::nullopt;
    }
    accumulator = (accumulator << 6) | static_cast<uint32_t>(value);
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out.push_back(static_cast<uint8_t>((accumulator >> bits) & 0xffu));
    }
  }
  if (padding > 2 || bits >= 6) {
    if (error) *error = "invalid base64 length";
    return std::nullopt;
  }
  return out;
}

struct RawView {
  std::string id;
  std::string op;
  std::string page_id;
  std::string source_id;
  std::vector<std::string> part_ids;
  bool has_start = false;
  bool has_length = false;
  bool has_end = false;
  uint64_t start = 0;
  uint64_t length = 0;
  uint64_t end = 0;
  bool exported = false;
  std::vector<Interval> keep;
  std::vector<Interval> secret;
};

bool parseIntervalObject(const Json& value, Interval* out, std::string* error,
                         std::string_view where) {
  if (!value.is_object()) {
    if (error) *error = std::string(where) + ": interval must be an object";
    return false;
  }
  const Json* start_json = value.find("start");
  if (!start_json) start_json = value.find("offset");
  if (!start_json) {
    if (error) *error = std::string(where) + ": interval needs start/offset";
    return false;
  }
  uint64_t start = 0;
  try {
    start = start_json->as_u64();
  } catch (const std::exception& ex) {
    if (error) *error = std::string(where) + ": " + ex.what();
    return false;
  }
  const Json* length_json = value.find("length");
  const Json* end_json = value.find("end");
  uint64_t length = 0;
  if (length_json) {
    try {
      length = length_json->as_u64();
    } catch (const std::exception& ex) {
      if (error) *error = std::string(where) + ": " + ex.what();
      return false;
    }
  }
  if (end_json) {
    uint64_t end = 0;
    try {
      end = end_json->as_u64();
    } catch (const std::exception& ex) {
      if (error) *error = std::string(where) + ": " + ex.what();
      return false;
    }
    if (end < start) {
      if (error) *error = std::string(where) + ": end is before start";
      return false;
    }
    if (length_json && end - start != length) {
      if (error) *error = std::string(where) + ": end and length disagree";
      return false;
    }
    length = end - start;
  } else if (!length_json) {
    if (error) *error = std::string(where) + ": interval needs length or end";
    return false;
  }
  if (start > kMaxExpandedLength || length > kMaxExpandedLength - start) {
    if (error) *error = std::string(where) + ": interval exceeds 2^48";
    return false;
  }
  out->start = start;
  out->length = length;
  return true;
}

bool parseIntervalArray(const Json& value, std::vector<Interval>* out,
                        std::string* error, std::string_view where) {
  if (!value.is_array()) {
    if (error) *error = std::string(where) + ": expected an array";
    return false;
  }
  out->clear();
  for (size_t i = 0; i < value.as_array().size(); ++i) {
    Interval interval;
    if (!parseIntervalObject(value.as_array()[i], &interval, error,
                             std::string(where) + "[" + std::to_string(i) + "]")) {
      return false;
    }
    out->push_back(interval);
  }
  return true;
}

bool parseRawView(const Json& value, size_t index, RawView* out, std::string* error) {
  std::string where = "views[" + std::to_string(index) + "]";
  if (!value.is_object()) {
    if (error) *error = where + ": expected object";
    return false;
  }
  auto id = readString(value, "id", error, where, true);
  if (!id) return false;
  out->id = *id;
  if (!validUtf8AsciiId(out->id)) {
    if (error) *error = where + ": id must be non-empty printable ASCII";
    return false;
  }
  auto op = readString(value, "op", error, where, true);
  if (!op) return false;
  out->op = *op;
  if (out->op == "page") {
    auto page = readString(value, "page", error, where, true);
    if (!page) return false;
    out->page_id = *page;
  } else if (out->op == "slice" || out->op == "reverse") {
    auto source = readString(value, "source", error, where, true);
    if (!source) return false;
    out->source_id = *source;
  } else if (out->op == "concat") {
    const Json* parts = value.find("parts");
    if (!parts || !parts->is_array()) {
      if (error) *error = where + ": concat needs a parts array";
      return false;
    }
    for (size_t i = 0; i < parts->as_array().size(); ++i) {
      const Json& part = parts->as_array()[i];
      if (!part.is_string()) {
        if (error) *error = where + ".parts[" + std::to_string(i) + "]: expected string";
        return false;
      }
      out->part_ids.push_back(part.as_string());
    }
  } else {
    if (error) *error = where + ": unknown op '" + out->op + "'";
    return false;
  }

  if (const Json* start = value.find("start")) {
    try {
      out->start = start->as_u64();
    } catch (const std::exception& ex) {
      if (error) *error = where + ".start: " + ex.what();
      return false;
    }
    out->has_start = true;
  }
  if (const Json* length = value.find("length")) {
    try {
      out->length = length->as_u64();
    } catch (const std::exception& ex) {
      if (error) *error = where + ".length: " + ex.what();
      return false;
    }
    out->has_length = true;
  }
  if (const Json* end = value.find("end")) {
    try {
      out->end = end->as_u64();
    } catch (const std::exception& ex) {
      if (error) *error = where + ".end: " + ex.what();
      return false;
    }
    out->has_end = true;
  }
  if (out->has_length && out->has_end) {
    if (out->end < out->start || out->end - out->start != out->length) {
      if (error) *error = where + ": end and length disagree";
      return false;
    }
  }
  if (const Json* keep = value.find("keep")) {
    if (!parseIntervalArray(*keep, &out->keep, error, where + ".keep")) return false;
    out->exported = true;
  }
  if (const Json* secret = value.find("secret")) {
    if (!parseIntervalArray(*secret, &out->secret, error, where + ".secret")) {
      return false;
    }
    out->exported = true;
  }
  if (const Json* exported = value.find("export")) {
    if (!exported->is_bool()) {
      if (error) *error = where + ".export: expected boolean";
      return false;
    }
    out->exported = out->exported || exported->as_bool();
  }
  return true;
}

uint64_t satAdd(uint64_t a, uint64_t b, bool* ok) {
  if (b > kMaxExpandedLength || a > kMaxExpandedLength - b) {
    *ok = false;
    return 0;
  }
  return a + b;
}

}  // namespace

const Page* Model::findPage(std::string_view id) const {
  for (const auto& page : pages) {
    if (page.id == id) return &page;
  }
  return nullptr;
}

const ViewDef* Model::findView(std::string_view id) const {
  for (const auto& view : views) {
    if (view.id == id) return &view;
  }
  return nullptr;
}

const ExportDef* Model::findExport(std::string_view id) const {
  for (const auto& item : exports) {
    if (item.id == id) return &item;
  }
  return nullptr;
}

LoadResult loadModel(std::string text) {
  LoadResult result;
  std::string parse_error;
  Json root = Json::parse(text, &parse_error);
  if (!parse_error.empty()) {
    addError(result, "JSON parse error: " + parse_error);
    return result;
  }
  if (!root.is_object()) {
    addError(result, "top-level JSON value must be an object");
    return result;
  }

  Model model;
  model.raw_text = std::move(text);
  model.sha256 = sha256_hex(model.raw_text);

  if (const Json* format = root.find("format")) {
    if (!format->is_string()) {
      addError(result, "format: expected string");
      return result;
    }
    model.format = format->as_string();
  }
  if (model.format.empty()) model.format = "gsb.redaction.input/v1";
  if (model.format != "gsb.redaction.input/v1" &&
      model.format != "gsb.redaction.input/v1+json") {
    addError(result, "unsupported input format '" + model.format + "'");
  }

  // Pages.
  const Json* pages_json = root.find("pages");
  if (!pages_json || !pages_json->is_array()) {
    addError(result, "pages: expected an array");
    return result;
  }
  std::unordered_map<std::string, size_t> page_ids;
  for (size_t i = 0; i < pages_json->as_array().size(); ++i) {
    const Json& item = pages_json->as_array()[i];
    std::string where = "pages[" + std::to_string(i) + "]";
    if (!item.is_object()) {
      addError(result, where + ": expected object");
      continue;
    }
    auto id = readString(item, "id", &parse_error, where, true);
    if (!id) {
      addError(result, parse_error);
      continue;
    }
    if (!validUtf8AsciiId(*id)) {
      addError(result, where + ": id must be non-empty printable ASCII");
      continue;
    }
    if (page_ids.contains(*id)) {
      addError(result, where + ": duplicate page id '" + *id + "'");
      continue;
    }
    Page page;
    page.id = *id;
    std::optional<std::vector<uint8_t>> data;
    if (const Json* hex = item.find("data_hex")) {
      if (!hex->is_string()) {
        addError(result, where + ".data_hex: expected string");
        continue;
      }
      data = decodeHex(hex->as_string(), &parse_error);
      if (!data) {
        addError(result, where + ".data_hex: " + parse_error);
        continue;
      }
    } else if (const Json* b64 = item.find("data_base64")) {
      if (!b64->is_string()) {
        addError(result, where + ".data_base64: expected string");
        continue;
      }
      data = decodeBase64(b64->as_string(), &parse_error);
      if (!data) {
        addError(result, where + ".data_base64: " + parse_error);
        continue;
      }
    }
    std::optional<uint64_t> declared_size;
    if (const Json* size = item.find("size")) {
      try {
        declared_size = size->as_u64();
      } catch (const std::exception& ex) {
        addError(result, where + ".size: " + ex.what());
        continue;
      }
    }
    if (data) {
      page.data = std::move(*data);
      page.has_data = true;
      page.size = page.data.size();
      if (declared_size && *declared_size != page.size) {
        addError(result, where + ": size does not match decoded data");
        continue;
      }
    } else if (declared_size) {
      page.size = *declared_size;
    } else {
      addError(result, where + ": needs size or data_hex/data_base64");
      continue;
    }
    page_ids.emplace(page.id, model.pages.size());
    model.pages.push_back(std::move(page));
  }
  if (!result.errors.empty()) return result;

  // Raw view descriptions.
  const Json* views_json = root.find("views");
  if (!views_json || !views_json->is_array()) {
    addError(result, "views: expected an array");
    return result;
  }
  std::vector<RawView> raws;
  std::unordered_map<std::string, size_t> raw_ids;
  for (size_t i = 0; i < views_json->as_array().size(); ++i) {
    RawView raw;
    if (!parseRawView(views_json->as_array()[i], i, &raw, &parse_error)) {
      addError(result, parse_error);
      continue;
    }
    if (raw_ids.contains(raw.id)) {
      addError(result, "views[" + std::to_string(i) + "]: duplicate view id '" +
                           raw.id + "'");
      continue;
    }
    raw_ids.emplace(raw.id, i);
    raws.push_back(std::move(raw));
  }
  if (!result.errors.empty()) return result;

  std::unordered_map<std::string, int> state;  // 0 unvisited, 1 visiting, 2 done
  std::unordered_map<std::string, size_t> view_index;

  auto resolve_view = [&](auto&& self, const std::string& id) -> std::optional<size_t> {
    auto state_it = state.find(id);
    int s = state_it == state.end() ? 0 : state_it->second;
    if (s == 1) {
      addError(result, "view graph contains a cycle at '" + id + "'");
      return std::nullopt;
    }
    if (s == 2) {
      auto it = view_index.find(id);
      if (it == view_index.end()) {
        addError(result, "internal view resolution error");
        return std::nullopt;
      }
      return it->second;
    }
    auto raw_it = raw_ids.find(id);
    if (raw_it == raw_ids.end()) {
      addError(result, "unknown view reference '" + id + "'");
      return std::nullopt;
    }
    state[id] = 1;
    const RawView& raw = raws[raw_it->second];
    ViewDef def;
    def.id = raw.id;
    bool ok = true;

    if (raw.op == "page") {
      auto page_it = page_ids.find(raw.page_id);
      if (page_it == page_ids.end()) {
        addError(result, "view '" + id + "': unknown page '" + raw.page_id + "'");
        ok = false;
      } else {
        def.kind = ViewKind::PageSlice;
        def.page = page_it->second;
        const Page& page = model.pages[def.page];
        uint64_t start = raw.has_start ? raw.start : 0;
        uint64_t length = 0;
        if (raw.has_end) {
          if (raw.end < start) {
            addError(result, "view '" + id + "': end is before start");
            ok = false;
          } else {
            length = raw.end - start;
          }
        } else if (raw.has_length) {
          length = raw.length;
        } else {
          if (start > page.size) {
            addError(result, "view '" + id + "': start exceeds page size");
            ok = false;
          } else {
            length = page.size - start;
          }
        }
        if (ok && (start > page.size || length > page.size - start)) {
          addError(result, "view '" + id + "': page slice out of bounds");
          ok = false;
        }
        if (ok && length > kMaxExpandedLength) {
          addError(result, "view '" + id + "': length exceeds 2^48");
          ok = false;
        }
        def.page_offset = start;
        def.length = length;
      }
    } else if (raw.op == "concat") {
      def.kind = ViewKind::Concat;
      def.prefix.push_back(0);
      uint64_t total = 0;
      for (const std::string& part_id : raw.part_ids) {
        auto part = self(self, part_id);
        if (!part) {
          ok = false;
          break;
        }
        uint64_t part_length = model.views[*part].length;
        bool add_ok = true;
        total = satAdd(total, part_length, &add_ok);
        if (!add_ok) {
          addError(result, "view '" + id + "': concatenated length exceeds 2^48");
          ok = false;
          break;
        }
        def.parts.push_back(*part);
        def.prefix.push_back(total);
      }
      def.length = total;
    } else if (raw.op == "slice") {
      auto source = self(self, raw.source_id);
      if (!source) {
        ok = false;
      } else {
        def.kind = ViewKind::Slice;
        def.source = *source;
        uint64_t source_length = model.views[*source].length;
        uint64_t start = raw.has_start ? raw.start : 0;
        uint64_t length = 0;
        if (raw.has_end) {
          if (raw.end < start) {
            addError(result, "view '" + id + "': end is before start");
            ok = false;
          } else {
            length = raw.end - start;
          }
        } else if (raw.has_length) {
          length = raw.length;
        } else {
          if (start > source_length) {
            addError(result, "view '" + id + "': start exceeds source length");
            ok = false;
          } else {
            length = source_length - start;
          }
        }
        if (ok && (start > source_length || length > source_length - start)) {
          addError(result, "view '" + id + "': slice out of bounds");
          ok = false;
        }
        if (ok && length > kMaxExpandedLength) {
          addError(result, "view '" + id + "': length exceeds 2^48");
          ok = false;
        }
        def.source_offset = start;
        def.length = length;
      }
    } else if (raw.op == "reverse") {
      auto source = self(self, raw.source_id);
      if (!source) {
        ok = false;
      } else {
        def.kind = ViewKind::Reverse;
        def.source = *source;
        def.length = model.views[*source].length;
      }
    }

    if (!ok) return std::nullopt;
    view_index[id] = model.views.size();
    model.views.push_back(std::move(def));
    state[id] = 2;
    return view_index[id];
  };

  for (const RawView& raw : raws) {
    auto resolved = resolve_view(resolve_view, raw.id);
    if (!resolved) {
      return result;
    }
    (void)resolved;
  }
  if (!result.errors.empty()) return result;

  // Exports.
  std::unordered_map<std::string, size_t> export_ids;
  auto add_export = [&](std::string id, size_t view, std::vector<Interval> keep,
                        std::vector<Interval> secret, std::string* error) -> bool {
    if (!validUtf8AsciiId(id)) {
      if (error) *error = "export id must be non-empty printable ASCII";
      return false;
    }
    if (export_ids.contains(id)) {
      if (error) *error = "duplicate export id '" + id + "'";
      return false;
    }
    uint64_t view_length = model.views[view].length;
    for (const auto& interval : keep) {
      if (interval.start > view_length || interval.length > view_length - interval.start) {
        if (error) *error = "keep interval out of bounds for export '" + id + "'";
        return false;
      }
    }
    for (const auto& interval : secret) {
      if (interval.start > view_length || interval.length > view_length - interval.start) {
        if (error) *error = "secret interval out of bounds for export '" + id + "'";
        return false;
      }
    }
    ExportDef def;
    def.id = std::move(id);
    def.view = view;
    def.keep = normalizeIntervals(std::move(keep));
    def.secret = normalizeIntervals(std::move(secret));
    export_ids.emplace(def.id, model.exports.size());
    model.exports.push_back(std::move(def));
    return true;
  };

  if (const Json* exports_json = root.find("exports")) {
    if (!exports_json->is_array()) {
      addError(result, "exports: expected an array");
      return result;
    }
    for (size_t i = 0; i < exports_json->as_array().size(); ++i) {
      const Json& item = exports_json->as_array()[i];
      std::string where = "exports[" + std::to_string(i) + "]";
      if (!item.is_object()) {
        addError(result, where + ": expected object");
        continue;
      }
      std::string id;
      if (const Json* id_json = item.find("id")) {
        if (!id_json->is_string()) {
          addError(result, where + ".id: expected string");
          continue;
        }
        id = id_json->as_string();
      } else {
        id = "e" + std::to_string(model.exports.size());
      }
      auto view_id = readString(item, "view", &parse_error, where, true);
      if (!view_id) {
        addError(result, parse_error);
        continue;
      }
      auto view_it = view_index.find(*view_id);
      if (view_it == view_index.end()) {
        addError(result, where + ": unknown view '" + *view_id + "'");
        continue;
      }
      std::vector<Interval> keep;
      std::vector<Interval> secret;
      if (const Json* keep_json = item.find("keep")) {
        if (!parseIntervalArray(*keep_json, &keep, &parse_error, where + ".keep")) {
          addError(result, parse_error);
          continue;
        }
      }
      if (const Json* secret_json = item.find("secret")) {
        if (!parseIntervalArray(*secret_json, &secret, &parse_error, where + ".secret")) {
          addError(result, parse_error);
          continue;
        }
      }
      if (!add_export(id, view_it->second, std::move(keep), std::move(secret),
                      &parse_error)) {
        addError(result, where + ": " + parse_error);
      }
    }
  } else {
    for (const RawView& raw : raws) {
      if (!raw.exported) continue;
      auto view_it = view_index.find(raw.id);
      if (view_it == view_index.end()) continue;
      if (!add_export(raw.id, view_it->second, raw.keep, raw.secret, &parse_error)) {
        addError(result, "view '" + raw.id + "': " + parse_error);
      }
    }
  }
  if (!result.errors.empty()) return result;

  // Edit items.
  const Json* edits_json = root.find("edits");
  if (!edits_json) edits_json = root.find("edit_items");
  if (edits_json) {
    if (!edits_json->is_array()) {
      addError(result, "edits: expected an array");
      return result;
    }
    if (edits_json->as_array().size() > kMaxEditItems) {
      addError(result, "edits: at most 22 edit items are allowed");
      return result;
    }
    struct RawEdit {
      std::optional<int64_t> id;
      size_t view = 0;
      Interval interval;
      uint64_t cost = 0;
    };
    std::vector<RawEdit> raw_edits;
    std::set<int64_t> used_ids;
    for (size_t i = 0; i < edits_json->as_array().size(); ++i) {
      const Json& item = edits_json->as_array()[i];
      std::string where = "edits[" + std::to_string(i) + "]";
      if (!item.is_object()) {
        addError(result, where + ": expected object");
        continue;
      }
      RawEdit edit;
      if (const Json* id_json = item.find("id")) {
        try {
          edit.id = id_json->as_i64();
        } catch (const std::exception& ex) {
          addError(result, where + ".id: " + ex.what());
          continue;
        }
        if (*edit.id < 0) {
          addError(result, where + ".id: must be non-negative");
          continue;
        }
        if (!used_ids.insert(*edit.id).second) {
          addError(result, where + ".id: duplicate edit id");
          continue;
        }
      }
      auto view_id = readString(item, "view", &parse_error, where, true);
      if (!view_id) {
        addError(result, parse_error);
        continue;
      }
      auto view_it = view_index.find(*view_id);
      if (view_it == view_index.end()) {
        addError(result, where + ": unknown view '" + *view_id + "'");
        continue;
      }
      edit.view = view_it->second;
      if (!parseIntervalObject(item, &edit.interval, &parse_error, where)) {
        addError(result, parse_error);
        continue;
      }
      if (edit.interval.start > model.views[edit.view].length ||
          edit.interval.length >
              model.views[edit.view].length - edit.interval.start) {
        addError(result, where + ": edit interval out of bounds");
        continue;
      }
      if (const Json* cost = item.find("cost")) {
        try {
          edit.cost = cost->as_u64();
        } catch (const std::exception& ex) {
          addError(result, where + ".cost: " + ex.what());
          continue;
        }
        if (edit.cost == std::numeric_limits<uint64_t>::max()) {
          addError(result, where + ".cost: UINT64_MAX is reserved for infeasibility");
          continue;
        }
      }
      raw_edits.push_back(edit);
    }
    if (!result.errors.empty()) return result;

    int64_t next_id = 0;
    for (RawEdit& edit : raw_edits) {
      if (edit.id) continue;
      while (used_ids.contains(next_id)) ++next_id;
      edit.id = next_id;
      used_ids.insert(next_id);
      ++next_id;
    }
    for (RawEdit& edit : raw_edits) {
      EditItem item;
      item.id = *edit.id;
      item.view = edit.view;
      item.interval = edit.interval;
      item.cost = edit.cost;
      model.items.push_back(item);
    }
    std::sort(model.items.begin(), model.items.end(),
              [](const EditItem& a, const EditItem& b) { return a.id < b.id; });
  }

  // Solver configuration.
  auto readBudget = [&](const Json& object, std::string_view key) -> std::optional<uint64_t> {
    const Json* value = object.find(key);
    if (!value) return std::nullopt;
    try {
      return value->as_u64();
    } catch (const std::exception& ex) {
      addError(result, std::string(key) + ": " + ex.what());
      return std::nullopt;
    }
  };
  if (const Json* solver = root.find("solver"); solver && solver->is_object()) {
    if (auto budget = readBudget(*solver, "node_budget")) model.config.node_budget = *budget;
  }
  if (auto budget = readBudget(root, "node_budget")) model.config.node_budget = *budget;
  if (auto budget = readBudget(root, "search_budget")) model.config.node_budget = *budget;
  if (auto max_inspect = readBudget(root, "max_inspect")) {
    model.config.max_inspect = *max_inspect;
  }
  if (!result.errors.empty()) return result;

  result.model = std::move(model);
  return result;
}

void SecretSet::add(size_t page, uint64_t start, uint64_t end) {
  if (start >= end) return;
  ranges_[page].push_back({start, end});
  empty_ = false;
  finished_ = false;
}

void SecretSet::finish() {
  if (finished_) return;
  for (auto& [page, ranges] : ranges_) {
    (void)page;
    std::sort(ranges.begin(), ranges.end(),
              [](const PageRange& a, const PageRange& b) {
                return a.start < b.start || (a.start == b.start && a.end < b.end);
              });
    std::vector<PageRange> merged;
    for (const PageRange& range : ranges) {
      if (merged.empty() || range.start > merged.back().end) {
        merged.push_back(range);
      } else if (range.end > merged.back().end) {
        merged.back().end = range.end;
      }
    }
    ranges = std::move(merged);
  }
  finished_ = true;
}

bool SecretSet::contains(size_t page, uint64_t offset) const {
  const std::vector<PageRange>* list = ranges(page);
  if (!list) return false;
  auto it = std::upper_bound(list->begin(), list->end(), offset,
                             [](uint64_t value, const PageRange& range) {
                               return value < range.start;
                             });
  if (it == list->begin()) return false;
  --it;
  return offset >= it->start && offset < it->end;
}

bool SecretSet::intersects(size_t page, uint64_t start, uint64_t end) const {
  if (start >= end) return false;
  const std::vector<PageRange>* list = ranges(page);
  if (!list) return false;
  auto it = std::lower_bound(list->begin(), list->end(), start,
                             [](const PageRange& range, uint64_t value) {
                               return range.end <= value;
                             });
  return it != list->end() && it->start < end;
}

const std::vector<PageRange>* SecretSet::ranges(size_t page) const {
  auto it = ranges_.find(page);
  return it == ranges_.end() ? nullptr : &it->second;
}

namespace {

using IdentityKey = std::tuple<size_t, uint64_t, uint64_t>;

size_t identityHash(const IdentityKey& key) {
  uint64_t h = std::hash<uint64_t>{}(static_cast<uint64_t>(std::get<0>(key)));
  h ^= std::hash<uint64_t>{}(std::get<1>(key)) + 0x9e3779b97f4a7c15ull +
       (h << 6) + (h >> 2);
  h ^= std::hash<uint64_t>{}(std::get<2>(key)) + 0x9e3779b97f4a7c15ull +
       (h << 6) + (h >> 2);
  return static_cast<size_t>(h);
}

void collectIdentities(const Model& model, size_t view, uint64_t start,
                       uint64_t length, SecretSet& secret,
                       std::unordered_set<IdentityKey, size_t (*)(const IdentityKey&)>&
                           seen) {
  if (length == 0) return;
  IdentityKey key{view, start, length};
  if (!seen.insert(key).second) return;

  const ViewDef& def = model.views[view];
  switch (def.kind) {
    case ViewKind::PageSlice:
      secret.add(def.page, def.page_offset + start, def.page_offset + start + length);
      break;
    case ViewKind::Slice:
      collectIdentities(model, def.source, def.source_offset + start, length, secret,
                        seen);
      break;
    case ViewKind::Reverse:
      collectIdentities(model, def.source, def.length - (start + length), length, secret,
                        seen);
      break;
    case ViewKind::Concat: {
      uint64_t end = start + length;
      auto it = std::upper_bound(def.prefix.begin(), def.prefix.end(), start);
      size_t part = static_cast<size_t>(it - def.prefix.begin());
      if (part > 0) --part;
      while (part < def.parts.size() && def.prefix[part] < end) {
        uint64_t part_start = def.prefix[part];
        uint64_t part_end = def.prefix[part + 1];
        uint64_t lo = std::max(start, part_start);
        uint64_t hi = std::min(end, part_end);
        if (lo < hi) {
          collectIdentities(model, def.parts[part], lo - part_start, hi - lo, secret,
                            seen);
        }
        ++part;
      }
      break;
    }
  }
}

}  // namespace

SecretSet computeSecretSet(const Model& model) {
  SecretSet secret;
  std::unordered_set<IdentityKey, size_t (*)(const IdentityKey&)> seen(
      0, identityHash);
  for (const ExportDef& export_def : model.exports) {
    for (const Interval& interval : export_def.secret) {
      collectIdentities(model, export_def.view, interval.start, interval.length, secret,
                        seen);
    }
  }
  secret.finish();
  return secret;
}

std::vector<Interval> normalizeIntervals(std::vector<Interval> intervals) {
  std::vector<Interval> non_empty;
  non_empty.reserve(intervals.size());
  for (const Interval& interval : intervals) {
    if (!interval.empty()) non_empty.push_back(interval);
  }
  std::sort(non_empty.begin(), non_empty.end(),
            [](const Interval& a, const Interval& b) {
              return a.start < b.start || (a.start == b.start && a.length < b.length);
            });
  std::vector<Interval> out;
  for (const Interval& interval : non_empty) {
    if (out.empty() || interval.start > out.back().end()) {
      out.push_back(interval);
    } else {
      uint64_t end = std::max(out.back().end(), interval.end());
      out.back().length = end - out.back().start;
    }
  }
  return out;
}

bool intervalsOverlap(const Interval& a, const Interval& b) {
  return a.start < b.end() && b.start < a.end();
}

Json intervalToJson(const Interval& interval) {
  Json::Object object;
  object.emplace("start", Json(interval.start));
  object.emplace("length", Json(interval.length));
  return Json(std::move(object));
}

std::optional<Interval> parseIntervalJson(const Json& value, std::string* error) {
  Interval interval;
  if (!parseIntervalObject(value, &interval, error, "interval")) {
    return std::nullopt;
  }
  return interval;
}

}  // namespace gsb
