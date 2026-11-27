#include "ja/joint_admission.hpp"

#include <algorithm>
#include <cstring>
#include <limits>
#include <map>
#include <set>
#include <stdexcept>
#include <string>
#include <unordered_map>

namespace ja {
namespace {

using Wide = __int128;

constexpr std::uint32_t rotate_right(std::uint32_t value,
                                     std::uint32_t count) {
  return (value >> count) | (value << (32 - count));
}

class Sha256State {
 public:
  Sha256State() { reset(); }

  void reset() {
    state_[0] = 0x6a09e667u;
    state_[1] = 0xbb67ae85u;
    state_[2] = 0x3c6ef372u;
    state_[3] = 0xa54ff53au;
    state_[4] = 0x510e527fu;
    state_[5] = 0x9b05688cu;
    state_[6] = 0x1f83d9abu;
    state_[7] = 0x5be0cd19u;
    buffered_ = 0;
    bit_length_ = 0;
  }

  void update(const std::uint8_t* data, std::size_t size) {
    if (size == 0) {
      return;
    }
    bit_length_ += static_cast<std::uint64_t>(size) * 8u;
    while (size > 0) {
      const std::size_t take = std::min<std::size_t>(size, 64 - buffered_);
      std::memcpy(block_ + buffered_, data, take);
      buffered_ += take;
      data += take;
      size -= take;
      if (buffered_ == 64) {
        transform(block_);
        buffered_ = 0;
      }
    }
  }

  Digest finish() {
    const std::uint64_t original_bits = bit_length_;
    const std::uint8_t pad = 0x80;
    update(&pad, 1);
    const std::uint8_t zero = 0;
    while (buffered_ != 56) {
      update(&zero, 1);
    }
    std::uint8_t length_bytes[8];
    for (int i = 0; i < 8; ++i) {
      length_bytes[7 - i] =
          static_cast<std::uint8_t>(original_bits >> (i * 8));
    }
    update(length_bytes, sizeof(length_bytes));

    Digest result{};
    for (std::size_t i = 0; i < 8; ++i) {
      result[i * 4] = static_cast<std::uint8_t>(state_[i] >> 24);
      result[i * 4 + 1] = static_cast<std::uint8_t>(state_[i] >> 16);
      result[i * 4 + 2] = static_cast<std::uint8_t>(state_[i] >> 8);
      result[i * 4 + 3] = static_cast<std::uint8_t>(state_[i]);
    }
    reset();
    return result;
  }

 private:
  static constexpr std::uint32_t kRound[64] = {
      0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
      0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
      0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
      0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
      0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
      0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
      0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
      0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
      0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
      0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
      0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
      0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
      0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
  };

  static void transform(const std::uint8_t* block,
                        std::uint32_t* state) {
    std::uint32_t words[64]{};
    for (std::size_t i = 0; i < 16; ++i) {
      words[i] = (static_cast<std::uint32_t>(block[i * 4]) << 24) |
                 (static_cast<std::uint32_t>(block[i * 4 + 1]) << 16) |
                 (static_cast<std::uint32_t>(block[i * 4 + 2]) << 8) |
                 static_cast<std::uint32_t>(block[i * 4 + 3]);
    }
    for (std::size_t i = 16; i < 64; ++i) {
      const std::uint32_t s0 = rotate_right(words[i - 15], 7) ^
                               rotate_right(words[i - 15], 18) ^
                               (words[i - 15] >> 3);
      const std::uint32_t s1 = rotate_right(words[i - 2], 17) ^
                               rotate_right(words[i - 2], 19) ^
                               (words[i - 2] >> 10);
      words[i] = words[i - 16] + s0 + words[i - 7] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t i = 0; i < 64; ++i) {
      const std::uint32_t s1 =
          rotate_right(e, 6) ^ rotate_right(e, 11) ^ rotate_right(e, 25);
      const std::uint32_t choose = (e & f) ^ ((~e) & g);
      const std::uint32_t temp1 =
          h + s1 + choose + kRound[i] + words[i];
      const std::uint32_t s0 =
          rotate_right(a, 2) ^ rotate_right(a, 13) ^ rotate_right(a, 22);
      const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t temp2 = s0 + majority;
      h = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
  }

  void transform(const std::uint8_t* block) { transform(block, state_); }

  std::uint32_t state_[8]{};
  std::uint64_t bit_length_ = 0;
  std::uint8_t block_[64]{};
  std::size_t buffered_ = 0;
};

class Writer {
 public:
  void u8(std::uint8_t value) { bytes_.push_back(value); }

  void u16(std::uint16_t value) {
    for (int shift = 8; shift >= 0; shift -= 8) {
      bytes_.push_back(static_cast<std::uint8_t>(value >> shift));
    }
  }

  void u32(std::uint32_t value) {
    for (int shift = 24; shift >= 0; shift -= 8) {
      bytes_.push_back(static_cast<std::uint8_t>(value >> shift));
    }
  }

  void u64(std::uint64_t value) {
    for (int shift = 56; shift >= 0; shift -= 8) {
      bytes_.push_back(static_cast<std::uint8_t>(value >> shift));
    }
  }

  void i64(std::int64_t value) {
    u64(static_cast<std::uint64_t>(value));
  }

  void raw(const char* text) {
    while (*text != '\0') {
      bytes_.push_back(static_cast<std::uint8_t>(*text++));
    }
  }

  void span(const Span& value) {
    i64(value.begin.constant);
    i64(value.begin.per_length);
    i64(value.end.constant);
    i64(value.end.per_length);
    u8(value.clamp_end_to_length ? 1 : 0);
  }

  void field(const Field& value) {
    u32(value.id);
    u32(value.obligation);
    span(value.span);
    u32(value.shared_object);
    u32(value.interpretation);
  }

  std::vector<std::uint8_t> take() { return std::move(bytes_); }

 private:
  std::vector<std::uint8_t> bytes_;
};

Wide eval_linear(const Linear& value, std::uint64_t length) {
  return static_cast<Wide>(value.constant) +
         static_cast<Wide>(value.per_length) * static_cast<Wide>(length);
}

struct EvaluatedSpan {
  Wide begin = 0;
  Wide end = 0;
};

EvaluatedSpan eval_span(const Span& span, std::uint64_t length) {
  EvaluatedSpan result;
  result.begin = eval_linear(span.begin, length);
  result.end = eval_linear(span.end, length);
  if (span.clamp_end_to_length &&
      result.end > static_cast<Wide>(length)) {
    result.end = static_cast<Wide>(length);
  }
  return result;
}

bool valid_span_at(const Span& span, std::uint64_t length) {
  const EvaluatedSpan evaluated = eval_span(span, length);
  return evaluated.begin >= 0 && evaluated.end >= evaluated.begin &&
         evaluated.end <= static_cast<Wide>(length);
}

bool span_contains_at(const Span& outer,
                      const Span& inner,
                      std::uint64_t length) {
  const EvaluatedSpan a = eval_span(outer, length);
  const EvaluatedSpan b = eval_span(inner, length);
  return b.begin >= a.begin && b.end <= a.end;
}

std::optional<Interval> to_interval(const EvaluatedSpan& value) {
  if (value.begin < 0 || value.end < value.begin ||
      value.end > static_cast<Wide>(
                       std::numeric_limits<std::uint64_t>::max())) {
    return std::nullopt;
  }
  return Interval{static_cast<std::uint64_t>(value.begin),
                  static_cast<std::uint64_t>(value.end)};
}

bool same_span_expr(const Span& lhs, const Span& rhs) {
  return lhs.begin.constant == rhs.begin.constant &&
         lhs.begin.per_length == rhs.begin.per_length &&
         lhs.end.constant == rhs.end.constant &&
         lhs.end.per_length == rhs.end.per_length &&
         lhs.clamp_end_to_length == rhs.clamp_end_to_length;
}

std::int64_t saturating_add(std::int64_t value, std::uint64_t delta) {
  const Wide sum = static_cast<Wide>(value) + static_cast<Wide>(delta);
  if (sum > static_cast<Wide>(std::numeric_limits<std::int64_t>::max())) {
    return std::numeric_limits<std::int64_t>::max();
  }
  if (sum < static_cast<Wide>(std::numeric_limits<std::int64_t>::min())) {
    return std::numeric_limits<std::int64_t>::min();
  }
  return static_cast<std::int64_t>(sum);
}

std::uint64_t floor_div(Wide numerator, Wide denominator) {
  Wide quotient = numerator / denominator;
  const Wide remainder = numerator % denominator;
  if (remainder != 0 && ((remainder < 0) != (denominator < 0))) {
    --quotient;
  }
  return static_cast<std::uint64_t>(quotient);
}

class CriticalLengths {
 public:
  CriticalLengths() {
    add_expression(Linear::fixed(0));
    add_expression(Linear::length());
  }

  void add(const Span& span) {
    add_expression(span.begin);
    add_expression(span.end);
  }

  void add(const Layout& layout) {
    if (layout.discriminant.has_value()) {
      add(layout.discriminant->span);
    }
    for (const SharedObject& object : layout.shared_objects) {
      add(object.span);
    }
    for (const Field& field : layout.common_fields) {
      add(field.span);
    }
    for (const Arm& arm : layout.arms) {
      for (const Field& field : arm.fields) {
        add(field.span);
      }
    }
    for (const Compatibility& compatibility : layout.compatibility) {
      for (const NewFragment& fragment : compatibility.new_fragments) {
        const Field* new_field = nullptr;
        for (const Field& field : layout.common_fields) {
          if (field.id == fragment.new_field) {
            new_field = &field;
            break;
          }
        }
        if (new_field == nullptr) {
          for (const Arm& arm : layout.arms) {
            for (const Field& field : arm.fields) {
              if (field.id == fragment.new_field) {
                new_field = &field;
                break;
              }
            }
            if (new_field != nullptr) {
              break;
            }
          }
        }
        if (new_field == nullptr) {
          continue;
        }
        add_expression(
            Linear{saturating_add(new_field->span.begin.constant,
                                  fragment.offset),
                   new_field->span.begin.per_length});
        if (fragment.size.has_value()) {
          add_expression(
              Linear{saturating_add(
                         saturating_add(new_field->span.begin.constant,
                                        fragment.offset),
                         *fragment.size),
                     new_field->span.begin.per_length});
        }
      }
    }
    boundaries_.insert(layout.min_length);
    boundaries_.insert(layout.max_length);
  }

  void add(const VisibilityManifest& manifest) {
    for (const PermissionRule& rule : manifest.grants) {
      add(rule.span);
    }
    for (const PermissionRule& rule : manifest.denials) {
      add(rule.span);
    }
  }

  std::vector<std::uint64_t> finish() const {
    std::set<std::uint64_t> all = boundaries_;
    all.insert(0);
    all.insert(kMaxMessageLength);

    for (const Linear& lhs : expressions_) {
      for (const Linear& rhs : expressions_) {
        const Wide delta_constant =
            static_cast<Wide>(lhs.constant) - static_cast<Wide>(rhs.constant);
        const Wide delta_slope =
            static_cast<Wide>(lhs.per_length) -
            static_cast<Wide>(rhs.per_length);
        if (delta_slope == 0) {
          continue;
        }
        const Wide numerator = -delta_constant;
        const std::uint64_t raw = floor_div(numerator, delta_slope);
        if (raw <= kMaxMessageLength) {
          for (std::int64_t delta = -1; delta <= 1; ++delta) {
            const Wide candidate =
                static_cast<Wide>(raw) + static_cast<Wide>(delta);
            if (candidate >= 0 &&
                candidate <= static_cast<Wide>(kMaxMessageLength)) {
              all.insert(static_cast<std::uint64_t>(candidate));
            }
          }
          const Wide ceil_value = static_cast<Wide>(raw) + 1;
          if (ceil_value >= 0 &&
              ceil_value <= static_cast<Wide>(kMaxMessageLength)) {
            all.insert(static_cast<std::uint64_t>(ceil_value));
          }
        }
      }
    }
    return std::vector<std::uint64_t>(all.begin(), all.end());
  }

 private:
  void add_expression(const Linear& expression) {
    expressions_.push_back(expression);
    boundaries_.insert(static_cast<std::uint64_t>(0));
  }

  std::vector<Linear> expressions_;
  std::set<std::uint64_t> boundaries_;
};

template <typename Predicate>
std::optional<Failure> for_each_length(const std::uint64_t first,
                                       const std::uint64_t last,
                                       const std::vector<std::uint64_t>& points,
                                       Predicate&& predicate) {
  if (first > last) {
    return std::nullopt;
  }

  std::vector<std::uint64_t> local;
  local.push_back(first);
  local.push_back(last);
  for (const std::uint64_t point : points) {
    if (point >= first && point <= last) {
      local.push_back(point);
    }
  }
  std::sort(local.begin(), local.end());
  local.erase(std::unique(local.begin(), local.end()), local.end());

  for (std::size_t i = 0; i < local.size(); ++i) {
    if (const auto failure = predicate(local[i])) {
      return failure;
    }
    if (i + 1 < local.size() && local[i + 1] > local[i] + 1) {
      const std::uint64_t middle =
          local[i] + (local[i + 1] - local[i]) / 2;
      if (const auto failure = predicate(middle)) {
        return failure;
      }
    }
  }
  return std::nullopt;
}

struct LayoutInfo {
  const Layout* layout = nullptr;
  std::unordered_map<FieldId, const Field*> fields;
  std::unordered_map<SharedId, const SharedObject*> shared;
  std::map<CaseId, const Arm*> arms;
  std::vector<CaseId> cases;
};

LayoutInfo inspect_layout(const Layout& layout) {
  LayoutInfo info;
  info.layout = &layout;
  for (const SharedObject& object : layout.shared_objects) {
    info.shared.emplace(object.id, &object);
  }
  for (const Field& field : layout.common_fields) {
    info.fields.emplace(field.id, &field);
  }
  for (const Arm& arm : layout.arms) {
    info.cases.push_back(arm.case_id);
    info.arms.emplace(arm.case_id, &arm);
    for (const Field& field : arm.fields) {
      info.fields.emplace(field.id, &field);
    }
  }
  return info;
}

bool field_is_common(const Layout& layout, FieldId id) {
  for (const Field& field : layout.common_fields) {
    if (field.id == id) {
      return true;
    }
  }
  return false;
}

bool field_is_active(const LayoutInfo& info,
                     const Field& field,
                     CaseId case_id) {
  if (field_is_common(*info.layout, field.id)) {
    return true;
  }
  const Arm* arm = nullptr;
  const auto found = info.arms.find(case_id);
  if (found != info.arms.end()) {
    arm = found->second;
  }
  if (arm == nullptr) {
    return false;
  }
  for (const Field& candidate : arm->fields) {
    if (&candidate == &field) {
      return true;
    }
  }
  return false;
}

const Field* find_field_for_case(const LayoutInfo& info,
                                 FieldId id,
                                 CaseId case_id) {
  const auto found = info.fields.find(id);
  if (found == info.fields.end() || !field_is_active(info, *found->second,
                                                      case_id)) {
    return nullptr;
  }
  return found->second;
}

std::optional<Failure> fail(Obligation layout, Obligation permission = 0) {
  return Failure{layout, permission};
}

bool id_in_cases(const LayoutInfo& info, CaseId case_id) {
  return info.arms.find(case_id) != info.arms.end();
}

bool span_valid_for_domain(const Span& span,
                           const Layout& layout,
                           const std::vector<std::uint64_t>& points) {
  return !for_each_length(
      layout.min_length, layout.max_length, points,
      [&](std::uint64_t length) -> std::optional<Failure> {
        if (!valid_span_at(span, length)) {
          return Failure{};
        }
        return std::nullopt;
      });
}

bool spans_overlap_somewhere(const Span& lhs,
                             const Span& rhs,
                             const Layout& layout,
                             const std::vector<std::uint64_t>& points) {
  bool overlap = false;
  for_each_length(
      layout.min_length, layout.max_length, points,
      [&](std::uint64_t length) -> std::optional<Failure> {
        const EvaluatedSpan a = eval_span(lhs, length);
        const EvaluatedSpan b = eval_span(rhs, length);
        if (a.begin < b.end && b.begin < a.end) {
          overlap = true;
          return Failure{};
        }
        return std::nullopt;
      });
  return overlap;
}

bool span_always_contains(const Span& outer,
                          const Span& inner,
                          const Layout& layout,
                          const std::vector<std::uint64_t>& points) {
  bool contained = true;
  const auto failure = for_each_length(
      layout.min_length, layout.max_length, points,
      [&](std::uint64_t length) -> std::optional<Failure> {
        if (!span_contains_at(outer, inner, length)) {
          contained = false;
          return Failure{};
        }
        return std::nullopt;
      });
  return contained && !failure.has_value();
}

bool tag_values_representable(std::uint8_t width,
                              std::uint64_t first,
                              std::uint64_t last) {
  if (width == 0 || width > 8 || first > last) {
    return false;
  }
  if (width == 8) {
    return true;
  }
  const std::uint64_t limit = std::uint64_t{1} << (width * 8);
  return last < limit;
}

}  // namespace

Digest sha256(const std::uint8_t* data, std::size_t size) {
  Sha256State state;
  state.update(data, size);
  return state.finish();
}

Digest sha256(const std::vector<std::uint8_t>& data) {
  return sha256(data.data(), data.size());
}

std::vector<std::uint8_t> canonical_layout(const Layout& layout) {
  Writer writer;
  writer.raw("JA-LAYOUT-1");
  writer.u32(layout.obligation);
  writer.u64(layout.min_length);
  writer.u64(layout.max_length);

  writer.u8(layout.discriminant.has_value() ? 1 : 0);
  if (layout.discriminant.has_value()) {
    const Discriminant& discriminant = *layout.discriminant;
    writer.u32(discriminant.obligation);
    writer.span(discriminant.span);
    writer.u8(discriminant.width);
    writer.u64(discriminant.domain_first);
    writer.u64(discriminant.domain_last);
    writer.u64(discriminant.bindings.size());
    for (const TagBinding& binding : discriminant.bindings) {
      writer.u32(binding.obligation);
      writer.u64(binding.first);
      writer.u64(binding.last);
      writer.u32(binding.case_id);
    }
  }

  writer.u64(layout.shared_objects.size());
  for (const SharedObject& object : layout.shared_objects) {
    writer.u32(object.id);
    writer.u32(object.obligation);
    writer.span(object.span);
    writer.u32(object.interpretation);
  }

  writer.u64(layout.common_fields.size());
  for (const Field& field : layout.common_fields) {
    writer.field(field);
  }

  writer.u64(layout.arms.size());
  for (const Arm& arm : layout.arms) {
    writer.u32(arm.case_id);
    writer.u32(arm.obligation);
    writer.u64(arm.fields.size());
    for (const Field& field : arm.fields) {
      writer.field(field);
    }
  }

  writer.u64(layout.compatibility.size());
  for (const Compatibility& compatibility : layout.compatibility) {
    writer.u32(compatibility.obligation);
    writer.u32(compatibility.old_field);
    writer.u64(compatibility.new_fragments.size());
    for (const NewFragment& fragment : compatibility.new_fragments) {
      writer.u32(fragment.obligation);
      writer.u32(fragment.new_field);
      writer.u64(fragment.offset);
      writer.u8(fragment.size.has_value() ? 1 : 0);
      if (fragment.size.has_value()) {
        writer.u64(*fragment.size);
      }
    }
  }
  return writer.take();
}

std::vector<std::uint8_t> canonical_manifest(
    const VisibilityManifest& manifest) {
  Writer writer;
  writer.raw("JA-VISIBILITY-1");
  writer.u64(manifest.generation);
  writer.u64(manifest.grants.size());
  for (const PermissionRule& rule : manifest.grants) {
    writer.u32(rule.obligation);
    writer.u8(static_cast<std::uint8_t>(rule.view));
    writer.span(rule.span);
  }
  writer.u64(manifest.denials.size());
  for (const PermissionRule& rule : manifest.denials) {
    writer.u32(rule.obligation);
    writer.u8(static_cast<std::uint8_t>(rule.view));
    writer.span(rule.span);
  }
  return writer.take();
}

Digest layout_digest(const Layout& layout) {
  return sha256(canonical_layout(layout));
}

namespace {

std::vector<const Field*> all_fields(const LayoutInfo& info) {
  std::vector<const Field*> result;
  for (const Field& field : info.layout->common_fields) {
    result.push_back(&field);
  }
  for (const Arm& arm : info.layout->arms) {
    for (const Field& field : arm.fields) {
      result.push_back(&field);
    }
  }
  return result;
}

std::optional<Failure> validate_layout(
    const LayoutInfo& info,
    const std::vector<std::uint64_t>& points) {
  const Layout& layout = *info.layout;
  if (layout.min_length > layout.max_length ||
      layout.max_length > kMaxMessageLength) {
    return fail(layout.obligation);
  }
  if (layout.arms.empty()) {
    return fail(layout.obligation);
  }

  std::set<CaseId> cases;
  for (const Arm& arm : layout.arms) {
    if (arm.case_id == 0 || !cases.insert(arm.case_id).second) {
      return fail(arm.obligation != 0 ? arm.obligation : layout.obligation);
    }
  }

  std::set<FieldId> field_ids;
  for (const Field* field : all_fields(info)) {
    if (!field_ids.insert(field->id).second) {
      return fail(field->obligation);
    }
    if (!span_valid_for_domain(field->span, layout, points)) {
      return fail(field->obligation);
    }
  }

  std::set<SharedId> shared_ids;
  std::unordered_map<SharedId, SemanticId> shared_interpretations;
  for (const SharedObject& object : layout.shared_objects) {
    if (object.id == 0 || !shared_ids.insert(object.id).second) {
      return fail(object.obligation);
    }
    if (!span_valid_for_domain(object.span, layout, points)) {
      return fail(object.obligation);
    }
  }

  for (const Field* field : all_fields(info)) {
    if (field->shared_object == 0) {
      continue;
    }
    const auto found = info.shared.find(field->shared_object);
    if (found == info.shared.end()) {
      return fail(field->obligation);
    }
    if (!span_always_contains(found->second->span, field->span, layout,
                              points)) {
      return fail(field->obligation);
    }
    if (field->interpretation != 0) {
      const auto existing =
          shared_interpretations.find(field->shared_object);
      if (existing == shared_interpretations.end()) {
        shared_interpretations.emplace(field->shared_object,
                                        field->interpretation);
      } else if (existing->second != field->interpretation) {
        return fail(field->obligation);
      }
    }
    if (found->second->interpretation != 0 &&
        field->interpretation != 0 &&
        found->second->interpretation != field->interpretation) {
      return fail(field->obligation);
    }
  }

  for (const Arm& arm : layout.arms) {
    std::vector<const Field*> active;
    for (const Field& field : layout.common_fields) {
      active.push_back(&field);
    }
    for (const Field& field : arm.fields) {
      active.push_back(&field);
    }
    for (std::size_t i = 0; i < active.size(); ++i) {
      for (std::size_t j = i + 1; j < active.size(); ++j) {
        const Field& lhs = *active[i];
        const Field& rhs = *active[j];
        if (lhs.interpretation != 0 && rhs.interpretation != 0 &&
            lhs.interpretation != rhs.interpretation &&
            spans_overlap_somewhere(lhs.span, rhs.span, layout, points)) {
          return fail(rhs.obligation);
        }
        if (lhs.shared_object != 0 && rhs.shared_object != 0 &&
            lhs.shared_object != rhs.shared_object &&
            same_span_expr(lhs.span, rhs.span)) {
          return fail(rhs.obligation);
        }
      }
    }
  }

  if (layout.discriminant.has_value()) {
    const Discriminant& discriminant = *layout.discriminant;
    if (discriminant.width == 0 || discriminant.width > 8 ||
        !tag_values_representable(discriminant.width,
                                  discriminant.domain_first,
                                  discriminant.domain_last)) {
      return fail(discriminant.obligation);
    }
    if (discriminant.span.begin.per_length != 0 ||
        discriminant.span.end.per_length != 0 ||
        discriminant.span.clamp_end_to_length) {
      return fail(discriminant.obligation);
    }
    const EvaluatedSpan tag_span = eval_span(discriminant.span, 0);
    if (tag_span.begin < 0 ||
        tag_span.end - tag_span.begin !=
            static_cast<Wide>(discriminant.width) ||
        tag_span.end > static_cast<Wide>(layout.min_length)) {
      return fail(discriminant.obligation);
    }
    if (discriminant.bindings.empty()) {
      return fail(discriminant.obligation);
    }

    std::vector<TagBinding> bindings = discriminant.bindings;
    std::sort(bindings.begin(), bindings.end(),
              [](const TagBinding& lhs, const TagBinding& rhs) {
                if (lhs.first != rhs.first) {
                  return lhs.first < rhs.first;
                }
                return lhs.last < rhs.last;
              });

    std::uint64_t cursor = discriminant.domain_first;
    bool covered = false;
    std::set<CaseId> referenced_cases;
    for (std::size_t i = 0; i < bindings.size(); ++i) {
      const TagBinding& binding = bindings[i];
      if (binding.first != cursor || binding.last < binding.first ||
          binding.last > discriminant.domain_last ||
          !id_in_cases(info, binding.case_id)) {
        return fail(binding.obligation != 0 ? binding.obligation
                                            : discriminant.obligation);
      }
      referenced_cases.insert(binding.case_id);
      if (binding.last == discriminant.domain_last) {
        if (i + 1 != bindings.size()) {
          return fail(bindings[i + 1].obligation != 0
                          ? bindings[i + 1].obligation
                          : discriminant.obligation);
        }
        covered = true;
        break;
      }
      cursor = binding.last + 1;
    }
    if (!covered) {
      return fail(discriminant.obligation);
    }
    if (referenced_cases.size() != cases.size()) {
      return fail(discriminant.obligation);
    }
  } else if (layout.arms.size() != 1) {
    return fail(layout.obligation);
  }

  std::set<FieldId> compatibility_fields;
  for (const Compatibility& compatibility : layout.compatibility) {
    if (!field_ids.count(compatibility.old_field) ||
        compatibility.new_fragments.empty() ||
        !compatibility_fields.insert(compatibility.old_field).second) {
      return fail(compatibility.obligation);
    }
    for (const NewFragment& fragment : compatibility.new_fragments) {
      if (fragment.offset > kMaxMessageLength ||
          (fragment.size.has_value() &&
           *fragment.size > kMaxMessageLength)) {
        return fail(compatibility.obligation);
      }
    }
  }

  return std::nullopt;
}

std::optional<Failure> verify_signature(
    const Layout& old_layout,
    const SignedManifest& signed_manifest,
    const ManifestVerifier& verifier) {
  if (!verifier) {
    return fail(old_layout.obligation, signed_manifest.signature_obligation);
  }
  try {
    if (!verifier(signed_manifest.manifest, signed_manifest.signature)) {
      return fail(old_layout.obligation,
                  signed_manifest.signature_obligation);
    }
  } catch (...) {
    return fail(old_layout.obligation, signed_manifest.signature_obligation);
  }
  return std::nullopt;
}

std::vector<TagClass> canonical_tags(const LayoutInfo& info, View view) {
  std::vector<TagClass> result;
  if (!info.layout->discriminant.has_value()) {
    return result;
  }
  for (const TagBinding& binding : info.layout->discriminant->bindings) {
    result.push_back(TagClass{binding.obligation, view, binding.first,
                              binding.last, binding.case_id});
  }
  return result;
}

std::vector<LengthClass> canonical_lengths(const LayoutInfo& info,
                                           View view) {
  std::vector<LengthClass> result;
  for (const CaseId case_id : info.cases) {
    result.push_back(LengthClass{info.layout->obligation, view, case_id,
                                 info.layout->min_length,
                                 info.layout->max_length});
  }
  return result;
}

bool ranges_overlap(std::uint64_t lhs_first,
                    std::uint64_t lhs_last,
                    std::uint64_t rhs_first,
                    std::uint64_t rhs_last) {
  return lhs_first <= rhs_last && rhs_first <= lhs_last;
}

bool class_matches_tag_map(const LayoutInfo& info,
                           const TagClass& tag_class) {
  if (!info.layout->discriminant.has_value()) {
    return false;
  }
  bool saw_value = false;
  for (const TagBinding& binding : info.layout->discriminant->bindings) {
    if (ranges_overlap(tag_class.first, tag_class.last, binding.first,
                       binding.last)) {
      saw_value = true;
      if (binding.case_id != tag_class.case_id) {
        return false;
      }
    }
  }
  return saw_value;
}

std::optional<Failure> check_tag_partition(const LayoutInfo& info,
                                           View view,
                                           const AdmissionProof& proof) {
  std::vector<TagClass> classes;
  for (const TagClass& tag_class : proof.tags) {
    if (tag_class.view == view) {
      classes.push_back(tag_class);
    }
  }

  if (!info.layout->discriminant.has_value()) {
    if (!classes.empty()) {
      return fail(classes.front().obligation);
    }
    return std::nullopt;
  }

  const Discriminant& discriminant = *info.layout->discriminant;
  if (classes.empty()) {
    return fail(discriminant.obligation);
  }
  std::sort(classes.begin(), classes.end(),
            [](const TagClass& lhs, const TagClass& rhs) {
              if (lhs.first != rhs.first) {
                return lhs.first < rhs.first;
              }
              return lhs.last < rhs.last;
            });

  std::uint64_t cursor = discriminant.domain_first;
  bool covered = false;
  for (std::size_t i = 0; i < classes.size(); ++i) {
    const TagClass& tag_class = classes[i];
    if (tag_class.first != cursor ||
        tag_class.last < tag_class.first ||
        tag_class.last > discriminant.domain_last ||
        !id_in_cases(info, tag_class.case_id) ||
        !class_matches_tag_map(info, tag_class)) {
      return fail(tag_class.obligation != 0 ? tag_class.obligation
                                            : discriminant.obligation);
    }
    if (tag_class.last == discriminant.domain_last) {
      if (i + 1 != classes.size()) {
        return fail(classes[i + 1].obligation != 0
                        ? classes[i + 1].obligation
                        : discriminant.obligation);
      }
      covered = true;
      break;
    }
    cursor = tag_class.last + 1;
  }
  if (!covered) {
    return fail(discriminant.obligation);
  }
  return std::nullopt;
}

std::optional<Failure> check_length_partition(const LayoutInfo& info,
                                              View view,
                                              const AdmissionProof& proof) {
  std::map<CaseId, std::vector<LengthClass>> by_case;
  for (const LengthClass& length_class : proof.lengths) {
    if (length_class.view != view) {
      continue;
    }
    if (!id_in_cases(info, length_class.case_id)) {
      return fail(length_class.obligation);
    }
    by_case[length_class.case_id].push_back(length_class);
  }

  for (const CaseId case_id : info.cases) {
    auto found = by_case.find(case_id);
    if (found == by_case.end() || found->second.empty()) {
      return fail(info.layout->obligation);
    }
    std::vector<LengthClass>& classes = found->second;
    std::sort(classes.begin(), classes.end(),
              [](const LengthClass& lhs, const LengthClass& rhs) {
                if (lhs.first != rhs.first) {
                  return lhs.first < rhs.first;
                }
                return lhs.last < rhs.last;
              });
    std::uint64_t cursor = info.layout->min_length;
    bool covered = false;
    for (std::size_t i = 0; i < classes.size(); ++i) {
      const LengthClass& length_class = classes[i];
      if (length_class.first != cursor ||
          length_class.last < length_class.first ||
          length_class.last > info.layout->max_length) {
        return fail(length_class.obligation != 0
                        ? length_class.obligation
                        : info.layout->obligation);
      }
      if (length_class.last == info.layout->max_length) {
        if (i + 1 != classes.size()) {
          return fail(classes[i + 1].obligation != 0
                          ? classes[i + 1].obligation
                          : info.layout->obligation);
        }
        covered = true;
        break;
      }
      cursor = length_class.last + 1;
    }
    if (!covered) {
      return fail(info.layout->obligation);
    }
  }
  return std::nullopt;
}

std::optional<Failure> check_proof_partition(
    const LayoutInfo& old_info,
    const LayoutInfo& new_info,
    const AdmissionProof& proof,
    const SignedManifest& signed_manifest) {
  if (proof.policy_generation != signed_manifest.manifest.generation) {
    return fail(old_info.layout->obligation);
  }
  for (const TagClass& tag_class : proof.tags) {
    if (tag_class.view != View::Old && tag_class.view != View::New) {
      return fail(tag_class.obligation);
    }
  }
  for (const LengthClass& length_class : proof.lengths) {
    if (length_class.view != View::Old &&
        length_class.view != View::New) {
      return fail(length_class.obligation);
    }
  }

  if (const auto failure = check_tag_partition(old_info, View::Old, proof)) {
    return failure;
  }
  if (const auto failure = check_tag_partition(new_info, View::New, proof)) {
    return failure;
  }
  if (const auto failure = check_length_partition(old_info, View::Old, proof)) {
    return failure;
  }
  if (const auto failure = check_length_partition(new_info, View::New, proof)) {
    return failure;
  }

  for (const TagClass& old_class : proof.tags) {
    if (old_class.view != View::Old) {
      continue;
    }
    for (const TagClass& new_class : proof.tags) {
      if (new_class.view != View::New) {
        continue;
      }
      if (ranges_overlap(old_class.first, old_class.last, new_class.first,
                         new_class.last) &&
          old_class.case_id != new_class.case_id) {
        return fail(old_class.obligation != 0 ? old_class.obligation
                                              : old_info.layout->obligation);
      }
    }
  }
  return std::nullopt;
}

struct ConcreteInterval {
  Wide begin = 0;
  Wide end = 0;
  Obligation obligation = 0;
};

std::vector<ConcreteInterval> concrete_rules(
    View view,
    const std::vector<PermissionRule>& rules,
    std::uint64_t length) {
  std::vector<ConcreteInterval> result;
  for (const PermissionRule& rule : rules) {
    if (rule.view != view) {
      continue;
    }
    const EvaluatedSpan evaluated = eval_span(rule.span, length);
    Wide begin = std::max<Wide>(evaluated.begin, 0);
    Wide end = std::min<Wide>(evaluated.end,
                             static_cast<Wide>(length));
    if (end < begin) {
      end = begin;
    }
    if (begin < end) {
      result.push_back(ConcreteInterval{begin, end, rule.obligation});
    }
  }
  return result;
}

std::optional<Failure> check_span_permission_at(
    View view,
    const Span& span,
    Obligation layout_obligation,
    const VisibilityManifest& manifest,
    std::uint64_t length) {
  if (!valid_span_at(span, length)) {
    return fail(layout_obligation);
  }
  const EvaluatedSpan read_span = eval_span(span, length);
  if (read_span.begin == read_span.end) {
    return std::nullopt;
  }

  const std::vector<ConcreteInterval> denials =
      concrete_rules(view, manifest.denials, length);
  for (const ConcreteInterval& denial : denials) {
    if (denial.begin < read_span.end && read_span.begin < denial.end) {
      return fail(layout_obligation, denial.obligation);
    }
  }

  std::vector<ConcreteInterval> grants =
      concrete_rules(view, manifest.grants, length);
  std::sort(grants.begin(), grants.end(),
            [](const ConcreteInterval& lhs,
               const ConcreteInterval& rhs) {
              if (lhs.begin != rhs.begin) {
                return lhs.begin < rhs.begin;
              }
              return lhs.end < rhs.end;
            });

  Wide cursor = read_span.begin;
  for (const ConcreteInterval& grant : grants) {
    if (grant.end <= cursor) {
      continue;
    }
    if (grant.begin > cursor) {
      break;
    }
    cursor = std::max(cursor, grant.end);
    if (cursor >= read_span.end) {
      return std::nullopt;
    }
  }
  return fail(layout_obligation);
}

std::optional<Failure> check_case_permissions(
    const LayoutInfo& info,
    View view,
    const VisibilityManifest& manifest,
    const std::vector<std::uint64_t>& points) {
  const Layout& layout = *info.layout;
  if (layout.discriminant.has_value()) {
    if (const auto failure = for_each_length(
            layout.min_length, layout.max_length, points,
            [&](std::uint64_t length) -> std::optional<Failure> {
              return check_span_permission_at(
                  view, layout.discriminant->span,
                  layout.discriminant->obligation, manifest, length);
            })) {
      return failure;
    }
  }

  for (const CaseId case_id : info.cases) {
    std::vector<const Field*> fields;
    for (const Field& field : layout.common_fields) {
      fields.push_back(&field);
    }
    const auto arm = info.arms.find(case_id);
    if (arm == info.arms.end()) {
      return fail(layout.obligation);
    }
    for (const Field& field : arm->second->fields) {
      fields.push_back(&field);
    }

    if (const auto failure = for_each_length(
            layout.min_length, layout.max_length, points,
            [&](std::uint64_t length) -> std::optional<Failure> {
              for (const Field* field : fields) {
                if (const auto field_failure = check_span_permission_at(
                        view, field->span, field->obligation, manifest,
                        length)) {
                  return field_failure;
                }
              }
              return std::nullopt;
            })) {
      return failure;
    }
  }
  return std::nullopt;
}

std::optional<Failure> check_compatibility(
    const LayoutInfo& old_info,
    const LayoutInfo& new_info,
    const std::vector<std::uint64_t>& points) {
  const Layout& old_layout = *old_info.layout;
  const Layout& new_layout = *new_info.layout;
  const std::uint64_t first =
      std::max(old_layout.min_length, new_layout.min_length);
  const std::uint64_t last =
      std::min(old_layout.max_length, new_layout.max_length);

  for (const Compatibility& compatibility : old_layout.compatibility) {
    const auto old_field_found = old_info.fields.find(compatibility.old_field);
    if (old_field_found == old_info.fields.end()) {
      return fail(compatibility.obligation);
    }
    const Field& old_field = *old_field_found->second;

    for (const CaseId old_case : old_info.cases) {
      if (!field_is_active(old_info, old_field, old_case)) {
        continue;
      }
      if (!id_in_cases(new_info, old_case)) {
        return fail(compatibility.obligation);
      }
      if (first > last) {
        return fail(compatibility.obligation);
      }

      if (const auto failure = for_each_length(
              first, last, points,
              [&](std::uint64_t length) -> std::optional<Failure> {
                const EvaluatedSpan old_span =
                    eval_span(old_field.span, length);
                if (old_span.begin < 0 ||
                    old_span.end < old_span.begin ||
                    old_span.end > static_cast<Wide>(length)) {
                  return fail(compatibility.obligation);
                }

                Wide cursor = old_span.begin;
                for (std::size_t i = 0;
                     i < compatibility.new_fragments.size(); ++i) {
                  const NewFragment& fragment =
                      compatibility.new_fragments[i];
                  const Field* new_field = find_field_for_case(
                      new_info, fragment.new_field, old_case);
                  if (new_field == nullptr) {
                    return fail(compatibility.obligation);
                  }
                  const EvaluatedSpan new_span =
                      eval_span(new_field->span, length);
                  if (new_span.begin < 0 ||
                      new_span.end < new_span.begin ||
                      new_span.end > static_cast<Wide>(length)) {
                    return fail(compatibility.obligation);
                  }

                  const Wide target_begin =
                      new_span.begin +
                      static_cast<Wide>(fragment.offset);
                  Wide target_end = new_span.end;
                  if (fragment.size.has_value()) {
                    target_end =
                        target_begin + static_cast<Wide>(*fragment.size);
                  }
                  if (target_begin < new_span.begin ||
                      target_end < target_begin ||
                      target_end > new_span.end ||
                      new_span.begin < old_span.begin ||
                      new_span.end > old_span.end ||
                      target_begin != cursor) {
                    return fail(compatibility.obligation);
                  }
                  cursor = target_end;
                }
                if (cursor != old_span.end) {
                  return fail(compatibility.obligation);
                }
                return std::nullopt;
              })) {
        return failure;
      }
    }
  }
  return std::nullopt;
}

std::optional<Failure> validate_manifest_rules(
    const Layout& old_layout,
    const VisibilityManifest& manifest) {
  for (const PermissionRule& rule : manifest.grants) {
    if (rule.view != View::Old && rule.view != View::New) {
      return fail(old_layout.obligation, rule.obligation);
    }
  }
  for (const PermissionRule& rule : manifest.denials) {
    if (rule.view != View::Old && rule.view != View::New) {
      return fail(old_layout.obligation, rule.obligation);
    }
  }
  return std::nullopt;
}

std::optional<Failure> analyze_admission(
    const LayoutInfo& old_info,
    const LayoutInfo& new_info,
    const SignedManifest& signed_manifest,
    const std::vector<std::uint64_t>& points) {
  if (const auto failure =
          validate_manifest_rules(*old_info.layout, signed_manifest.manifest)) {
    return failure;
  }
  if (const auto failure = check_case_permissions(
          old_info, View::Old, signed_manifest.manifest, points)) {
    return failure;
  }
  if (const auto failure = check_case_permissions(
          new_info, View::New, signed_manifest.manifest, points)) {
    return failure;
  }
  if (const auto failure =
          check_compatibility(old_info, new_info, points)) {
    return failure;
  }
  return std::nullopt;
}

AdmissionProof canonical_proof(const LayoutInfo& old_info,
                               const LayoutInfo& new_info,
                               Generation generation) {
  AdmissionProof proof;
  proof.policy_generation = generation;
  const std::vector<TagClass> old_tags =
      canonical_tags(old_info, View::Old);
  const std::vector<TagClass> new_tags =
      canonical_tags(new_info, View::New);
  proof.tags.insert(proof.tags.end(), old_tags.begin(), old_tags.end());
  proof.tags.insert(proof.tags.end(), new_tags.begin(), new_tags.end());
  const std::vector<LengthClass> old_lengths =
      canonical_lengths(old_info, View::Old);
  const std::vector<LengthClass> new_lengths =
      canonical_lengths(new_info, View::New);
  proof.lengths.insert(proof.lengths.end(), old_lengths.begin(),
                       old_lengths.end());
  proof.lengths.insert(proof.lengths.end(), new_lengths.begin(),
                       new_lengths.end());
  return proof;
}

std::optional<Failure> verify_impl(
    const Layout& old_layout,
    const Layout& new_layout,
    const SignedManifest& signed_manifest,
    const AdmissionProof& proof,
    const ManifestVerifier& verifier) {
  if (const auto failure =
          verify_signature(old_layout, signed_manifest, verifier)) {
    return failure;
  }

  const LayoutInfo old_info = inspect_layout(old_layout);
  const LayoutInfo new_info = inspect_layout(new_layout);
  CriticalLengths criticals;
  criticals.add(old_layout);
  criticals.add(new_layout);
  criticals.add(signed_manifest.manifest);
  const std::vector<std::uint64_t> points = criticals.finish();

  if (const auto failure = validate_layout(old_info, points)) {
    return failure;
  }
  if (const auto failure = validate_layout(new_info, points)) {
    return failure;
  }
  if (std::max(old_layout.min_length, new_layout.min_length) >
      std::min(old_layout.max_length, new_layout.max_length)) {
    return fail(old_layout.obligation);
  }
  if (std::set<CaseId>(old_info.cases.begin(), old_info.cases.end()) !=
      std::set<CaseId>(new_info.cases.begin(), new_info.cases.end())) {
    return fail(old_layout.obligation);
  }
  if (const auto failure = check_proof_partition(
          old_info, new_info, proof, signed_manifest)) {
    return failure;
  }
  return analyze_admission(old_info, new_info, signed_manifest, points);
}

std::optional<CaseId> select_case(const Layout& layout,
                                  std::uint64_t discriminant) {
  if (!layout.discriminant.has_value()) {
    if (layout.arms.size() != 1) {
      return std::nullopt;
    }
    return layout.arms.front().case_id;
  }
  for (const TagBinding& binding : layout.discriminant->bindings) {
    if (discriminant >= binding.first && discriminant <= binding.last) {
      return binding.case_id;
    }
  }
  return std::nullopt;
}

const Field* runtime_field(const Layout& layout,
                           CaseId case_id,
                           FieldId id) {
  for (const Field& field : layout.common_fields) {
    if (field.id == id) {
      return &field;
    }
  }
  for (const Arm& arm : layout.arms) {
    if (arm.case_id != case_id) {
      continue;
    }
    for (const Field& field : arm.fields) {
      if (field.id == id) {
        return &field;
      }
    }
  }
  return nullptr;
}

}  // namespace

struct ReadOnlyHandle::Impl {
  Layout old_layout;
  Layout new_layout;
  Digest old_digest{};
  Digest new_digest{};
  Generation generation = 0;
};

ReadOnlyHandle::ReadOnlyHandle(std::shared_ptr<const Impl> impl)
    : impl_(std::move(impl)) {}

bool ReadOnlyHandle::valid() const noexcept {
  return impl_ != nullptr;
}

const Digest& ReadOnlyHandle::old_layout_digest() const noexcept {
  static const Digest empty{};
  return impl_ == nullptr ? empty : impl_->old_digest;
}

const Digest& ReadOnlyHandle::new_layout_digest() const noexcept {
  static const Digest empty{};
  return impl_ == nullptr ? empty : impl_->new_digest;
}

Generation ReadOnlyHandle::policy_generation() const noexcept {
  return impl_ == nullptr ? 0 : impl_->generation;
}

std::optional<Interval> ReadOnlyHandle::interval(
    View view,
    FieldId field,
    std::uint64_t message_length,
    std::uint64_t discriminant) const {
  if (impl_ == nullptr) {
    return std::nullopt;
  }
  if (view != View::Old && view != View::New) {
    return std::nullopt;
  }
  const Layout& layout =
      view == View::Old ? impl_->old_layout : impl_->new_layout;
  if (message_length < layout.min_length ||
      message_length > layout.max_length) {
    return std::nullopt;
  }
  const std::optional<CaseId> case_id = select_case(layout, discriminant);
  if (!case_id.has_value()) {
    return std::nullopt;
  }
  const Field* selected = runtime_field(layout, *case_id, field);
  if (selected == nullptr ||
      !valid_span_at(selected->span, message_length)) {
    return std::nullopt;
  }
  return to_interval(eval_span(selected->span, message_length));
}

std::optional<ByteView> ReadOnlyHandle::read(View view,
                                             FieldId field,
                                             const Message& message) const {
  if (message.size > 0 && message.bytes == nullptr) {
    return std::nullopt;
  }
  const std::optional<Interval> resolved =
      interval(view, field, message.size, message.discriminant);
  if (!resolved.has_value() || resolved->end > message.size) {
    return std::nullopt;
  }
  if (resolved->end == resolved->begin) {
    return ByteView{message.bytes, 0};
  }
  if (resolved->begin >
      static_cast<std::uint64_t>(
          std::numeric_limits<std::size_t>::max())) {
    return std::nullopt;
  }
  return ByteView{message.bytes + static_cast<std::size_t>(resolved->begin),
                  resolved->end - resolved->begin};
}

JointAdmission::JointAdmission(ManifestVerifier verifier)
    : verifier_(std::move(verifier)) {}

std::optional<Failure> JointAdmission::verify(
    const Layout& old_layout,
    const Layout& new_layout,
    const SignedManifest& signed_manifest,
    const AdmissionProof& proof,
    const ManifestVerifier& verifier) {
  return verify_impl(old_layout, new_layout, signed_manifest, proof, verifier);
}

AdmissionOutcome JointAdmission::admit(
    const Layout& old_layout,
    const Layout& new_layout,
    const SignedManifest& signed_manifest,
    const AdmissionProof* proof) const {
  AdmissionProof synthesized;
  const AdmissionProof* selected = proof;
  if (selected == nullptr) {
    const LayoutInfo old_info = inspect_layout(old_layout);
    const LayoutInfo new_info = inspect_layout(new_layout);
    synthesized =
        canonical_proof(old_info, new_info, signed_manifest.manifest.generation);
    selected = &synthesized;
  }

  const std::optional<Failure> failure =
      verify_impl(old_layout, new_layout, signed_manifest, *selected,
                  verifier_);
  if (failure.has_value()) {
    AdmissionOutcome outcome;
    outcome.failure = *failure;
    return outcome;
  }

  auto impl = std::make_shared<ReadOnlyHandle::Impl>();
  impl->old_layout = old_layout;
  impl->new_layout = new_layout;
  impl->old_digest = layout_digest(old_layout);
  impl->new_digest = layout_digest(new_layout);
  impl->generation = signed_manifest.manifest.generation;

  AdmissionOutcome outcome;
  outcome.handle.emplace(ReadOnlyHandle(std::move(impl)));
  return outcome;
}

}  // namespace ja
