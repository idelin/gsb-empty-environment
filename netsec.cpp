#include "netsec.hpp"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <limits>
#include <new>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

// These limits are deliberately generous for ordinary modules but keep a
// hostile binary from turning a vector count into unbounded work.
constexpr std::uint32_t kMaxVectorLength = 16777216u;

bool fail(Error& error, ErrorCode code, std::size_t offset,
          std::string message) {
  error.code = code;
  error.offset = offset;
  error.message = std::move(message);
  return false;
}

class Reader {
 public:
  Reader() = default;

  Reader(const std::uint8_t* data, std::size_t size)
      : begin_(data), cursor_(data), end_(data + size) {}

  bool at_end() const { return cursor_ == end_; }

  std::size_t offset() const {
    return static_cast<std::size_t>(cursor_ - begin_);
  }

  std::size_t remaining() const {
    return static_cast<std::size_t>(end_ - cursor_);
  }

  bool read_byte(std::uint8_t& value) {
    if (cursor_ == end_) {
      return false;
    }
    value = *cursor_++;
    return true;
  }

  bool read_bytes(std::uint8_t* out, std::size_t count) {
    if (count > remaining()) {
      return false;
    }
    std::memcpy(out, cursor_, count);
    cursor_ += count;
    return true;
  }

  bool skip(std::size_t count) {
    if (count > remaining()) {
      return false;
    }
    cursor_ += count;
    return true;
  }

  bool read_u32(std::uint32_t& value) {
    std::uint32_t result = 0;
    for (unsigned index = 0; index < 5; ++index) {
      std::uint8_t byte = 0;
      if (!read_byte(byte)) {
        return false;
      }
      if (index == 4 && (byte & 0xf0u) != 0) {
        return false;
      }
      result |= static_cast<std::uint32_t>(byte & 0x7fu) << (7u * index);
      if ((byte & 0x80u) == 0) {
        value = result;
        return true;
      }
    }
    return false;
  }

  bool read_s32(std::int32_t& value) {
    std::uint64_t result = 0;
    unsigned shift = 0;
    for (unsigned index = 0; index < 5; ++index) {
      std::uint8_t byte = 0;
      if (!read_byte(byte)) {
        return false;
      }
      if (index == 4) {
        if ((byte & 0x80u) != 0) {
          return false;
        }
        const std::uint8_t upper = static_cast<std::uint8_t>(byte & 0x70u);
        if (upper != 0x00u && upper != 0x70u) {
          return false;
        }
      }
      result |= static_cast<std::uint64_t>(byte & 0x7fu) << shift;
      shift += 7;
      if ((byte & 0x80u) == 0) {
        if (shift < 64 && (byte & 0x40u) != 0) {
          result |= (~std::uint64_t{0}) << shift;
        }
        value = static_cast<std::int32_t>(result);
        return true;
      }
    }
    return false;
  }

  bool read_s64(std::int64_t& value) {
    std::uint64_t result = 0;
    unsigned shift = 0;
    for (unsigned index = 0; index < 10; ++index) {
      std::uint8_t byte = 0;
      if (!read_byte(byte)) {
        return false;
      }
      if (index == 9) {
        if ((byte & 0x80u) != 0) {
          return false;
        }
        const std::uint8_t upper = static_cast<std::uint8_t>(byte & 0x7eu);
        if (upper != 0x00u && upper != 0x7eu) {
          return false;
        }
      }
      result |= static_cast<std::uint64_t>(byte & 0x7fu) << shift;
      shift += 7;
      if ((byte & 0x80u) == 0) {
        if (shift < 64 && (byte & 0x40u) != 0) {
          result |= (~std::uint64_t{0}) << shift;
        }
        value = static_cast<std::int64_t>(result);
        return true;
      }
    }
    return false;
  }

  bool read_f32_bits(std::uint32_t& value) {
    std::uint8_t bytes[4];
    if (!read_bytes(bytes, sizeof(bytes))) {
      return false;
    }
    value = static_cast<std::uint32_t>(bytes[0]) |
            (static_cast<std::uint32_t>(bytes[1]) << 8) |
            (static_cast<std::uint32_t>(bytes[2]) << 16) |
            (static_cast<std::uint32_t>(bytes[3]) << 24);
    return true;
  }

  bool read_f64_bits(std::uint64_t& value) {
    std::uint8_t bytes[8];
    if (!read_bytes(bytes, sizeof(bytes))) {
      return false;
    }
    value = 0;
    for (unsigned index = 0; index < 8; ++index) {
      value |= static_cast<std::uint64_t>(bytes[index]) << (8u * index);
    }
    return true;
  }

  bool read_name(std::string& value) {
    std::uint32_t length = 0;
    if (!read_u32(length) || length > remaining()) {
      return false;
    }
    value.assign(reinterpret_cast<const char*>(cursor_), length);
    cursor_ += length;
    return true;
  }

  bool sub_reader(std::uint32_t length, Reader& out) {
    if (length > remaining()) {
      return false;
    }
    out = Reader(cursor_, length);
    cursor_ += length;
    return true;
  }

 private:
  const std::uint8_t* begin_ = nullptr;
  const std::uint8_t* cursor_ = nullptr;
  const std::uint8_t* end_ = nullptr;
};

bool is_value_type(std::uint8_t type) {
  switch (type) {
    case 0x7f:  // i32
    case 0x7e:  // i64
    case 0x7d:  // f32
    case 0x7c:  // f64
    case 0x7b:  // v128
    case 0x70:  // funcref
    case 0x6f:  // externref
      return true;
    default:
      return false;
  }
}

bool read_value_type(Reader& reader, std::uint8_t& type, Error& error) {
  if (!reader.read_byte(type)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated value type");
  }
  if (!is_value_type(type)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "unknown value type encoding");
  }
  return true;
}

bool read_ref_kind(Reader& reader, RefKind& kind, Error& error) {
  std::uint8_t value = 0;
  if (!reader.read_byte(value)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated reference type");
  }
  if (value == 0x70) {
    kind = RefKind::funcref;
    return true;
  }
  if (value == 0x6f) {
    kind = RefKind::externref;
    return true;
  }
  return fail(error, ErrorCode::malformed, reader.offset(),
              "unknown reference type encoding");
}

bool read_limits(Reader& reader, Limits& limits, Error& error) {
  std::uint32_t flags = 0;
  if (!reader.read_u32(flags) || flags > 1) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "invalid limits flags");
  }
  if (!reader.read_u32(limits.min)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated limits minimum");
  }
  limits.has_max = flags == 1;
  if (limits.has_max) {
    if (!reader.read_u32(limits.max)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated limits maximum");
    }
    if (limits.max < limits.min) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "limits maximum is smaller than minimum");
    }
  }
  return true;
}

bool read_table_type(Reader& reader, TableType& table, Error& error) {
  if (!read_ref_kind(reader, table.element_type, error)) {
    return false;
  }
  return read_limits(reader, table.limits, error);
}

bool read_global_type(Reader& reader, GlobalType& global, Error& error) {
  if (!read_value_type(reader, global.value_type, error)) {
    return false;
  }
  std::uint8_t mutability = 0;
  if (!reader.read_byte(mutability)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated global mutability");
  }
  if (mutability > 1) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "invalid global mutability");
  }
  global.mutable_ = mutability == 1;
  return true;
}

bool read_const_expr(Reader& reader, ConstExpr& expression, Error& error) {
  expression = ConstExpr{};
  expression.offset = reader.offset();
  while (true) {
    std::uint8_t opcode = 0;
    if (!reader.read_byte(opcode)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated constant expression");
    }
    switch (opcode) {
      case 0x0b: {  // end
        if (expression.instruction_count == 0) {
          return fail(error, ErrorCode::malformed, expression.offset,
                      "empty constant expression");
        }
        return true;
      }
      case 0x41: {  // i32.const
        std::int32_t value = 0;
        if (!reader.read_s32(value)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid i32 constant");
        }
        expression.kind = ConstExpr::Kind::i32_const;
        expression.integer = value;
        break;
      }
      case 0x42: {  // i64.const
        std::int64_t value = 0;
        if (!reader.read_s64(value)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid i64 constant");
        }
        expression.kind = ConstExpr::Kind::i64_const;
        expression.integer = value;
        break;
      }
      case 0x43: {  // f32.const
        std::uint32_t bits = 0;
        if (!reader.read_f32_bits(bits)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid f32 constant");
        }
        expression.kind = ConstExpr::Kind::f32_const;
        expression.bits = bits;
        break;
      }
      case 0x44: {  // f64.const
        std::uint64_t bits = 0;
        if (!reader.read_f64_bits(bits)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid f64 constant");
        }
        expression.kind = ConstExpr::Kind::f64_const;
        expression.bits = bits;
        break;
      }
      case 0x23: {  // global.get
        std::uint32_t index = 0;
        if (!reader.read_u32(index)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid global.get constant expression");
        }
        expression.kind = ConstExpr::Kind::global_get;
        expression.index = index;
        break;
      }
      case 0xd0: {  // ref.null
        RefKind kind = RefKind::funcref;
        if (!read_ref_kind(reader, kind, error)) {
          return false;
        }
        expression.kind = ConstExpr::Kind::ref_null;
        expression.ref_kind = kind;
        break;
      }
      case 0xd2: {  // ref.func
        std::uint32_t index = 0;
        if (!reader.read_u32(index)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "invalid ref.func constant expression");
        }
        expression.kind = ConstExpr::Kind::ref_func;
        expression.ref_kind = RefKind::funcref;
        expression.index = index;
        break;
      }
      default:
        return fail(error, ErrorCode::unsupported_feature, reader.offset(),
                    "unsupported instruction in constant expression");
    }
    ++expression.instruction_count;
  }
}

bool read_vec_count(Reader& reader, std::uint32_t& count, Error& error) {
  if (!reader.read_u32(count)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated vector length");
  }
  if (count > kMaxVectorLength) {
    return fail(error, ErrorCode::resource_limit, reader.offset(),
                "vector length exceeds implementation limit");
  }
  return true;
}

bool read_function_index_entries(Reader& reader, ElementSegment& segment,
                                 Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  segment.entries.clear();
  segment.entries.reserve(count);
  for (std::uint32_t index = 0; index < count; ++index) {
    std::uint32_t function_index = 0;
    if (!reader.read_u32(function_index)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated element function index");
    }
    ElementEntry entry;
    entry.is_null = false;
    entry.function_index = function_index;
    segment.entries.push_back(entry);
  }
  return true;
}

bool read_expression_entries(Reader& reader, ElementSegment& segment,
                             Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  segment.entries.clear();
  segment.entries.reserve(count);
  for (std::uint32_t index = 0; index < count; ++index) {
    ConstExpr expression;
    if (!read_const_expr(reader, expression, error)) {
      return false;
    }
    if (expression.instruction_count != 1) {
      return fail(error, ErrorCode::unsupported_feature, expression.offset,
                  "element entry must be a single ref.null or ref.func");
    }
    if (expression.ref_kind != segment.element_type) {
      return fail(error, ErrorCode::element_type_mismatch, expression.offset,
                  "element entry reference type does not match segment type");
    }
    ElementEntry entry;
    if (expression.kind == ConstExpr::Kind::ref_null) {
      entry.is_null = true;
      segment.entries.push_back(entry);
      continue;
    }
    if (expression.kind != ConstExpr::Kind::ref_func) {
      return fail(error, ErrorCode::unsupported_feature, expression.offset,
                  "element entry is not a function reference");
    }
    entry.is_null = false;
    entry.function_index = expression.index;
    segment.entries.push_back(entry);
  }
  return true;
}

bool read_element_kind(Reader& reader, Error& error) {
  std::uint8_t kind = 0;
  if (!reader.read_byte(kind)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated element kind");
  }
  if (kind != 0x00) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "invalid element kind");
  }
  return true;
}

bool parse_element_segment(Reader& reader, ElementSegment& segment,
                           Error& error) {
  segment = ElementSegment{};
  segment.offset_in_binary = reader.offset();

  std::uint32_t flags = 0;
  if (!reader.read_u32(flags)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated element segment flags");
  }
  if (flags > 7) {
    return fail(error, ErrorCode::malformed, segment.offset_in_binary,
                "unknown element segment flags");
  }
  segment.flags = flags;

  switch (flags) {
    case 0:
      segment.mode = ElementSegment::Mode::active;
      segment.table_index = 0;
      if (!read_const_expr(reader, segment.offset, error)) {
        return false;
      }
      return read_function_index_entries(reader, segment, error);

    case 1:
      segment.mode = ElementSegment::Mode::passive;
      if (!read_element_kind(reader, error)) {
        return false;
      }
      return read_function_index_entries(reader, segment, error);

    case 2:
      segment.mode = ElementSegment::Mode::active;
      if (!reader.read_u32(segment.table_index)) {
        return fail(error, ErrorCode::malformed, reader.offset(),
                    "truncated element table index");
      }
      if (!read_const_expr(reader, segment.offset, error) ||
          !read_element_kind(reader, error)) {
        return false;
      }
      return read_function_index_entries(reader, segment, error);

    case 3:
      segment.mode = ElementSegment::Mode::declarative;
      if (!read_element_kind(reader, error)) {
        return false;
      }
      return read_function_index_entries(reader, segment, error);

    case 4:
      segment.mode = ElementSegment::Mode::active;
      segment.table_index = 0;
      if (!read_const_expr(reader, segment.offset, error)) {
        return false;
      }
      return read_expression_entries(reader, segment, error);

    case 5:
      segment.mode = ElementSegment::Mode::passive;
      if (!read_ref_kind(reader, segment.element_type, error)) {
        return false;
      }
      return read_expression_entries(reader, segment, error);

    case 6:
      segment.mode = ElementSegment::Mode::active;
      if (!reader.read_u32(segment.table_index) ||
          !read_const_expr(reader, segment.offset, error) ||
          !read_ref_kind(reader, segment.element_type, error)) {
        if (error.code == ErrorCode::ok) {
          fail(error, ErrorCode::malformed, reader.offset(),
               "truncated element segment");
        }
        return false;
      }
      return read_expression_entries(reader, segment, error);

    case 7:
      segment.mode = ElementSegment::Mode::declarative;
      if (!read_ref_kind(reader, segment.element_type, error)) {
        return false;
      }
      return read_expression_entries(reader, segment, error);

    default:
      return fail(error, ErrorCode::malformed, segment.offset_in_binary,
                  "unknown element segment flags");
  }
}

int section_rank(std::uint8_t id) {
  // The data-count section (id 12) precedes the code section even though its
  // numeric id sorts after the data section.
  if (id == 12) {
    return 10;
  }
  if (id == 10) {
    return 11;
  }
  if (id == 11) {
    return 12;
  }
  return id;
}

bool parse_type_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  module.types.clear();
  module.types.reserve(count);
  for (std::uint32_t index = 0; index < count; ++index) {
    std::uint8_t form = 0;
    if (!reader.read_byte(form)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated function type");
    }
    if (form != 0x60) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "unsupported type form");
    }

    FunctionType type;
    std::uint32_t parameter_count = 0;
    if (!read_vec_count(reader, parameter_count, error)) {
      return false;
    }
    type.params.reserve(parameter_count);
    for (std::uint32_t parameter = 0; parameter < parameter_count;
         ++parameter) {
      std::uint8_t value_type = 0;
      if (!read_value_type(reader, value_type, error)) {
        return false;
      }
      type.params.push_back(value_type);
    }

    std::uint32_t result_count = 0;
    if (!read_vec_count(reader, result_count, error)) {
      return false;
    }
    type.results.reserve(result_count);
    for (std::uint32_t result = 0; result < result_count; ++result) {
      std::uint8_t value_type = 0;
      if (!read_value_type(reader, value_type, error)) {
        return false;
      }
      type.results.push_back(value_type);
    }
    module.types.push_back(std::move(type));
  }
  return true;
}

bool parse_import_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  for (std::uint32_t index = 0; index < count; ++index) {
    std::string module_name;
    std::string field_name;
    if (!reader.read_name(module_name) || !reader.read_name(field_name)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated import name");
    }

    std::uint8_t kind = 0;
    if (!reader.read_byte(kind)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated import description");
    }
    switch (kind) {
      case 0x00: {  // function
        std::uint32_t type_index = 0;
        if (!reader.read_u32(type_index)) {
          return fail(error, ErrorCode::malformed, reader.offset(),
                      "truncated imported function type");
        }
        if (type_index >= module.types.size()) {
          return fail(error, ErrorCode::type_index_out_of_bounds,
                      reader.offset(), "imported function type is invalid");
        }
        module.function_type_indices.push_back(type_index);
        ++module.imported_function_count;
        break;
      }
      case 0x01: {  // table
        TableType table;
        if (!read_table_type(reader, table, error)) {
          return false;
        }
        module.tables.push_back(table);
        ++module.imported_table_count;
        break;
      }
      case 0x02: {  // memory
        Limits limits;
        if (!read_limits(reader, limits, error)) {
          return false;
        }
        ++module.imported_memory_count;
        break;
      }
      case 0x03: {  // global
        GlobalType global;
        if (!read_global_type(reader, global, error)) {
          return false;
        }
        module.globals.push_back(global);
        break;
      }
      default:
        return fail(error, ErrorCode::malformed, reader.offset(),
                    "unknown import kind");
    }
  }
  return true;
}

bool parse_function_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  for (std::uint32_t index = 0; index < count; ++index) {
    std::uint32_t type_index = 0;
    if (!reader.read_u32(type_index)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated function type index");
    }
    if (type_index >= module.types.size()) {
      return fail(error, ErrorCode::type_index_out_of_bounds, reader.offset(),
                  "function type index is invalid");
    }
    module.function_type_indices.push_back(type_index);
  }
  return true;
}

bool parse_table_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  module.tables.reserve(module.tables.size() + count);
  for (std::uint32_t index = 0; index < count; ++index) {
    TableType table;
    if (!read_table_type(reader, table, error)) {
      return false;
    }
    module.tables.push_back(table);
  }
  return true;
}

bool parse_memory_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  for (std::uint32_t index = 0; index < count; ++index) {
    Limits limits;
    if (!read_limits(reader, limits, error)) {
      return false;
    }
  }
  module.defined_memory_count += count;
  return true;
}

bool parse_global_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  module.globals.reserve(module.globals.size() + count);
  for (std::uint32_t index = 0; index < count; ++index) {
    GlobalType global;
    if (!read_global_type(reader, global, error)) {
      return false;
    }
    ConstExpr initializer;
    if (!read_const_expr(reader, initializer, error)) {
      return false;
    }
    module.globals.push_back(global);
  }
  return true;
}

bool parse_export_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  module.exports.clear();
  module.exports.reserve(count);
  for (std::uint32_t index = 0; index < count; ++index) {
    Export entry;
    if (!reader.read_name(entry.name)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated export name");
    }
    std::uint8_t kind = 0;
    if (!reader.read_byte(kind) || kind > 3) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "invalid export kind");
    }
    entry.kind = static_cast<Export::Kind>(kind);
    if (!reader.read_u32(entry.index)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated export index");
    }
    module.exports.push_back(std::move(entry));
  }
  return true;
}

bool parse_start_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t function_index = 0;
  if (!reader.read_u32(function_index)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated start function index");
  }
  if (function_index >= module.function_count()) {
    return fail(error, ErrorCode::function_index_out_of_bounds,
                reader.offset(), "start function index is out of bounds");
  }
  module.has_start = true;
  module.start_function = function_index;
  return true;
}

bool parse_element_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  module.elements.clear();
  module.elements.reserve(count);
  for (std::uint32_t index = 0; index < count; ++index) {
    ElementSegment segment;
    if (!parse_element_segment(reader, segment, error)) {
      return false;
    }
    module.elements.push_back(std::move(segment));
  }
  return true;
}

bool parse_code_section(Reader& reader, Module& module, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  const std::uint32_t defined_functions =
      module.function_count() - module.imported_function_count;
  if (count != defined_functions) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "code section function count does not match function section");
  }
  for (std::uint32_t index = 0; index < count; ++index) {
    std::uint32_t body_size = 0;
    if (!reader.read_u32(body_size)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated function body size");
    }
    if (!reader.skip(body_size)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "function body exceeds code section");
    }
  }
  return true;
}

bool parse_data_section(Reader& reader, Error& error) {
  std::uint32_t count = 0;
  if (!read_vec_count(reader, count, error)) {
    return false;
  }
  for (std::uint32_t index = 0; index < count; ++index) {
    std::uint32_t flags = 0;
    if (!reader.read_u32(flags)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated data segment flags");
    }
    if (flags > 2) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "unknown data segment flags");
    }
    if (flags == 2) {
      std::uint32_t memory_index = 0;
      if (!reader.read_u32(memory_index)) {
        return fail(error, ErrorCode::malformed, reader.offset(),
                    "truncated data memory index");
      }
    }
    if (flags != 1) {
      ConstExpr offset;
      if (!read_const_expr(reader, offset, error)) {
        return false;
      }
    }
    std::uint32_t byte_count = 0;
    if (!read_vec_count(reader, byte_count, error) ||
        !reader.skip(byte_count)) {
      if (error.code == ErrorCode::ok) {
        fail(error, ErrorCode::malformed, reader.offset(),
             "truncated data bytes");
      }
      return false;
    }
  }
  return true;
}

bool parse_data_count_section(Reader& reader, Error& error) {
  std::uint32_t count = 0;
  if (!reader.read_u32(count)) {
    return fail(error, ErrorCode::malformed, reader.offset(),
                "truncated data count");
  }
  return true;
}

bool parse_sections(Reader& reader, Module& module, Error& error) {
  int last_rank = 0;
  while (!reader.at_end()) {
    std::uint8_t section_id = 0;
    if (!reader.read_byte(section_id)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated section id");
    }
    std::uint32_t section_size = 0;
    if (!reader.read_u32(section_size)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "truncated section size");
    }
    if (section_size > reader.remaining()) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "section exceeds module");
    }
    if (section_id == 0) {  // custom
      if (!reader.skip(section_size)) {
        return fail(error, ErrorCode::malformed, reader.offset(),
                    "truncated custom section");
      }
      continue;
    }
    if (section_id > 12) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "unknown section id");
    }
    const int rank = section_rank(section_id);
    if (rank <= last_rank) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "section is duplicated or out of order");
    }
    last_rank = rank;

    Reader section;
    if (!reader.sub_reader(section_size, section)) {
      return fail(error, ErrorCode::malformed, reader.offset(),
                  "section exceeds module");
    }
    bool ok = true;
    switch (section_id) {
      case 1:
        ok = parse_type_section(section, module, error);
        break;
      case 2:
        ok = parse_import_section(section, module, error);
        break;
      case 3:
        ok = parse_function_section(section, module, error);
        break;
      case 4:
        ok = parse_table_section(section, module, error);
        break;
      case 5:
        ok = parse_memory_section(section, module, error);
        break;
      case 6:
        ok = parse_global_section(section, module, error);
        break;
      case 7:
        ok = parse_export_section(section, module, error);
        break;
      case 8:
        ok = parse_start_section(section, module, error);
        break;
      case 9:
        ok = parse_element_section(section, module, error);
        break;
      case 10:
        ok = parse_code_section(section, module, error);
        break;
      case 11:
        ok = parse_data_section(section, error);
        break;
      case 12:
        ok = parse_data_count_section(section, error);
        break;
      default:
        ok = fail(error, ErrorCode::malformed, reader.offset(),
                  "unknown section");
        break;
    }
    if (!ok) {
      return false;
    }
    if (!section.at_end()) {
      return fail(error, ErrorCode::malformed, section.offset(),
                  "unexpected trailing bytes in section");
    }
  }
  return true;
}

void copy_error(const Error& error, char* buffer, std::size_t buffer_size) {
  if (buffer == nullptr || buffer_size == 0) {
    return;
  }
  std::string text;
  text = error_code_name(error.code);
  if (!error.message.empty()) {
    text += ": ";
    text += error.message;
  }
  if (text.size() >= buffer_size) {
    text.resize(buffer_size - 1);
  }
  std::memcpy(buffer, text.data(), text.size());
  buffer[text.size()] = '\0';
}

}  // namespace

const char* error_code_name(ErrorCode code) {
  switch (code) {
    case ErrorCode::ok:
      return "ok";
    case ErrorCode::invalid_magic:
      return "invalid_magic";
    case ErrorCode::invalid_version:
      return "invalid_version";
    case ErrorCode::malformed:
      return "malformed";
    case ErrorCode::unsupported_feature:
      return "unsupported_feature";
    case ErrorCode::type_index_out_of_bounds:
      return "type_index_out_of_bounds";
    case ErrorCode::function_index_out_of_bounds:
      return "function_index_out_of_bounds";
    case ErrorCode::table_index_out_of_bounds:
      return "table_index_out_of_bounds";
    case ErrorCode::table_offset_out_of_bounds:
      return "table_offset_out_of_bounds";
    case ErrorCode::element_function_index_out_of_bounds:
      return "element_function_index_out_of_bounds";
    case ErrorCode::element_type_mismatch:
      return "element_type_mismatch";
    case ErrorCode::resource_limit:
      return "resource_limit";
    case ErrorCode::internal_error:
      return "internal_error";
  }
  return "unknown";
}

bool element_function_index_is_valid(std::uint32_t function_index,
                                     std::uint32_t function_count) {
  return function_count != 0 && function_index < function_count;
}

ErrorCode decode_module(const std::uint8_t* data, std::size_t size, Module& out,
                        Error& error) {
  error = Error{};
  out = Module{};

  if (data == nullptr) {
    fail(error, ErrorCode::invalid_magic, 0, "module data is null");
    return error.code;
  }
  Reader reader(data, size);
  std::uint8_t header[8];
  if (!reader.read_bytes(header, sizeof(header))) {
    fail(error, ErrorCode::malformed, 0, "module header is truncated");
    return error.code;
  }
  if (header[0] != 0x00 || header[1] != 0x61 || header[2] != 0x73 ||
      header[3] != 0x6d) {
    fail(error, ErrorCode::invalid_magic, 0, "invalid WebAssembly magic");
    return error.code;
  }
  if (header[4] != 0x01 || header[5] != 0x00 || header[6] != 0x00 ||
      header[7] != 0x00) {
    fail(error, ErrorCode::invalid_version, 4,
         "unsupported WebAssembly binary version");
    return error.code;
  }

  try {
    if (!parse_sections(reader, out, error)) {
      return error.code;
    }
  } catch (const std::bad_alloc&) {
    fail(error, ErrorCode::resource_limit, reader.offset(),
         "not enough memory while decoding module");
    return error.code;
  } catch (const std::length_error&) {
    fail(error, ErrorCode::resource_limit, reader.offset(),
         "module structure exceeds addressable memory");
    return error.code;
  }
  return ErrorCode::ok;
}

ErrorCode decode_module(const std::vector<std::uint8_t>& data, Module& out,
                        Error& error) {
  return decode_module(data.data(), data.size(), out, error);
}

ErrorCode decode_and_validate(const std::uint8_t* data, std::size_t size,
                              Module& out, Error& error) {
  const ErrorCode decoded = decode_module(data, size, out, error);
  if (decoded != ErrorCode::ok) {
    return decoded;
  }
  return validate_module(out, error);
}

ErrorCode decode_and_validate(const std::vector<std::uint8_t>& data, Module& out,
                              Error& error) {
  return decode_and_validate(data.data(), data.size(), out, error);
}

ErrorCode validate_module(const Module& module, Error& error) {
  error = Error{};
  const std::uint32_t function_count = module.function_count();

  for (std::size_t segment_index = 0; segment_index < module.elements.size();
       ++segment_index) {
    const ElementSegment& segment = module.elements[segment_index];
    for (std::size_t entry_index = 0; entry_index < segment.entries.size();
         ++entry_index) {
      const ElementEntry& entry = segment.entries[entry_index];
      if (!entry.is_null && entry.function_index >= function_count) {
        fail(error, ErrorCode::element_function_index_out_of_bounds,
             segment.offset_in_binary,
             "element segment " + std::to_string(segment_index) +
                 " entry " + std::to_string(entry_index) +
                 " references function " +
                 std::to_string(entry.function_index) +
                 ", but function count is " +
                 std::to_string(function_count));
        return error.code;
      }
    }

    if (segment.mode != ElementSegment::Mode::active) {
      continue;
    }
    if (segment.table_index >= module.tables.size()) {
      fail(error, ErrorCode::table_index_out_of_bounds, segment.offset_in_binary,
           "element segment " + std::to_string(segment_index) +
               " references a table that does not exist");
      return error.code;
    }
    const TableType& table = module.tables[segment.table_index];
    if (table.element_type != segment.element_type) {
      fail(error, ErrorCode::element_type_mismatch, segment.offset_in_binary,
           "element segment type does not match table type");
      return error.code;
    }
    if (segment.offset.kind != ConstExpr::Kind::i32_const ||
        segment.offset.instruction_count != 1) {
      fail(error, ErrorCode::unsupported_feature, segment.offset.offset,
           "active element offset must be a single i32.const");
      return error.code;
    }
    const std::int64_t offset = segment.offset.integer;
    if (offset < 0 ||
        static_cast<std::uint64_t>(offset) >
            static_cast<std::uint64_t>(table.limits.min) ||
        static_cast<std::uint64_t>(segment.entries.size()) >
            static_cast<std::uint64_t>(table.limits.min) -
                static_cast<std::uint64_t>(offset)) {
      fail(error, ErrorCode::table_offset_out_of_bounds,
           segment.offset_in_binary,
           "element segment does not fit in its table");
      return error.code;
    }
  }

  for (const Export& entry : module.exports) {
    bool valid = false;
    switch (entry.kind) {
      case Export::Kind::function:
        valid = entry.index < function_count;
        break;
      case Export::Kind::table:
        valid = entry.index < module.tables.size();
        break;
      case Export::Kind::memory:
        valid = entry.index <
                module.imported_memory_count + module.defined_memory_count;
        break;
      case Export::Kind::global:
        valid = entry.index < module.globals.size();
        break;
    }
    if (!valid) {
      fail(error, ErrorCode::function_index_out_of_bounds, 0,
           "export " + entry.name + " references an invalid index");
      return error.code;
    }
  }
  return ErrorCode::ok;
}

ErrorCode instantiate_module(const Module& module, Instance& out,
                             Error& error) {
  error = Error{};
  const ErrorCode validation = validate_module(module, error);
  if (validation != ErrorCode::ok) {
    return validation;
  }

  try {
    out = Instance{};
    out.function_type_indices = module.function_type_indices;
    out.globals = module.globals;
    out.tables.reserve(module.tables.size());
    for (const TableType& table : module.tables) {
      TableInstance instance;
      instance.type = table;
      instance.entries.resize(table.limits.min);
      out.tables.push_back(std::move(instance));
    }
  } catch (const std::bad_alloc&) {
    fail(error, ErrorCode::resource_limit, 0,
         "not enough memory for declared table size");
    out = Instance{};
    return error.code;
  } catch (const std::length_error&) {
    fail(error, ErrorCode::resource_limit, 0,
         "declared table size exceeds addressable memory");
    out = Instance{};
    return error.code;
  }

  for (const ElementSegment& segment : module.elements) {
    if (segment.mode != ElementSegment::Mode::active) {
      continue;
    }
    TableInstance& table = out.tables[segment.table_index];
    const std::size_t offset = static_cast<std::size_t>(segment.offset.integer);
    for (std::size_t index = 0; index < segment.entries.size(); ++index) {
      const ElementEntry& entry = segment.entries[index];
      TableEntry& target = table.entries[offset + index];
      if (entry.is_null) {
        target.is_null = true;
        target.function_index = 0;
      } else {
        target.is_null = false;
        target.function_index = entry.function_index;
      }
    }
  }
  return ErrorCode::ok;
}

ErrorCode decode_and_instantiate(const std::uint8_t* data, std::size_t size,
                                 Instance& out, Error& error) {
  Module module;
  const ErrorCode decoded = decode_module(data, size, module, error);
  if (decoded != ErrorCode::ok) {
    return decoded;
  }
  return instantiate_module(module, out, error);
}

ErrorCode decode_and_instantiate(const std::vector<std::uint8_t>& data,
                                 Instance& out, Error& error) {
  return decode_and_instantiate(data.data(), data.size(), out, error);
}

ErrorCode decode_element_segment(const std::uint8_t* data, std::size_t size,
                                 std::uint32_t function_count,
                                 ElementSegment& out, Error& error) {
  error = Error{};
  out = ElementSegment{};
  if (data == nullptr) {
    fail(error, ErrorCode::malformed, 0, "element segment data is null");
    return error.code;
  }
  Reader reader(data, size);
  try {
    if (!parse_element_segment(reader, out, error)) {
      return error.code;
    }
  } catch (const std::bad_alloc&) {
    fail(error, ErrorCode::resource_limit, reader.offset(),
         "not enough memory while decoding element segment");
    return error.code;
  } catch (const std::length_error&) {
    fail(error, ErrorCode::resource_limit, reader.offset(),
         "element segment exceeds addressable memory");
    return error.code;
  }
  if (!reader.at_end()) {
    fail(error, ErrorCode::malformed, reader.offset(),
         "trailing bytes after element segment");
    return error.code;
  }
  for (std::size_t index = 0; index < out.entries.size(); ++index) {
    const ElementEntry& entry = out.entries[index];
    if (!entry.is_null && entry.function_index >= function_count) {
      fail(error, ErrorCode::element_function_index_out_of_bounds,
           out.offset_in_binary,
           "element segment entry " + std::to_string(index) +
               " references function " + std::to_string(entry.function_index) +
               ", but function count is " + std::to_string(function_count));
      return error.code;
    }
  }
  return ErrorCode::ok;
}

}  // namespace netsec

struct NetsecInstance {
  netsec::Instance instance;
};

extern "C" {

const char* netsec_error_code_name(int code) {
  if (code < 0 || code > static_cast<int>(netsec::ErrorCode::internal_error)) {
    return "unknown";
  }
  return netsec::error_code_name(static_cast<netsec::ErrorCode>(code));
}

int netsec_decode_module(const std::uint8_t* data, std::size_t size,
                         char* error_buffer, std::size_t error_buffer_size) {
  netsec::Module module;
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_and_validate(data, size, module, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
  }
  return static_cast<int>(result);
}

int netsec_parse_module(const std::uint8_t* data, std::size_t size,
                        char* error_buffer, std::size_t error_buffer_size) {
  netsec::Module module;
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_module(data, size, module, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
  }
  return static_cast<int>(result);
}

int netsec_validate_module(const std::uint8_t* data, std::size_t size,
                           char* error_buffer, std::size_t error_buffer_size) {
  netsec::Module module;
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_and_validate(data, size, module, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
  }
  return static_cast<int>(result);
}

NetsecInstance* netsec_instantiate_module(const std::uint8_t* data,
                                          std::size_t size,
                                          char* error_buffer,
                                          std::size_t error_buffer_size) {
  NetsecInstance* instance = new (std::nothrow) NetsecInstance();
  if (instance == nullptr) {
    if (error_buffer != nullptr && error_buffer_size != 0) {
      error_buffer[0] = '\0';
    }
    return nullptr;
  }
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_and_instantiate(data, size, instance->instance, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
    delete instance;
    return nullptr;
  }
  return instance;
}

int netsec_instantiate_module_into(const std::uint8_t* data, std::size_t size,
                                   NetsecInstance* instance,
                                   char* error_buffer,
                                   std::size_t error_buffer_size) {
  if (instance == nullptr) {
    if (error_buffer != nullptr && error_buffer_size != 0) {
      error_buffer[0] = '\0';
    }
    return static_cast<int>(netsec::ErrorCode::internal_error);
  }
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_and_instantiate(data, size, instance->instance, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
  }
  return static_cast<int>(result);
}

NetsecInstance* netsec_instance_create(void) {
  return new (std::nothrow) NetsecInstance();
}

void netsec_instance_destroy(NetsecInstance* instance) { delete instance; }

void netsec_instance_free(NetsecInstance* instance) { delete instance; }

std::uint32_t netsec_instance_function_count(const NetsecInstance* instance) {
  return instance == nullptr ? 0 : instance->instance.function_count();
}

std::uint32_t netsec_instance_table_count(const NetsecInstance* instance) {
  return instance == nullptr ? 0 : instance->instance.table_count();
}

std::uint32_t netsec_instance_table_length(const NetsecInstance* instance,
                                           std::uint32_t table_index) {
  if (instance == nullptr ||
      table_index >= instance->instance.tables.size()) {
    return 0;
  }
  return static_cast<std::uint32_t>(
      instance->instance.tables[table_index].entries.size());
}

int netsec_instance_table_entry(const NetsecInstance* instance,
                                std::uint32_t table_index,
                                std::uint32_t element_index,
                                std::uint32_t* function_index) {
  if (instance == nullptr ||
      table_index >= instance->instance.tables.size()) {
    return -1;
  }
  const netsec::TableInstance& table =
      instance->instance.tables[table_index];
  if (element_index >= table.entries.size()) {
    return -1;
  }
  const netsec::TableEntry& entry = table.entries[element_index];
  if (function_index != nullptr) {
    *function_index = entry.is_null ? 0 : entry.function_index;
  }
  return entry.is_null ? 0 : 1;
}

int netsec_decode_element_segment(const std::uint8_t* data, std::size_t size,
                                  std::uint32_t function_count,
                                  NetsecElementInfo* info,
                                  char* error_buffer,
                                  std::size_t error_buffer_size) {
  netsec::ElementSegment segment;
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_element_segment(data, size, function_count, segment, error);
  if (result != netsec::ErrorCode::ok) {
    netsec::copy_error(error, error_buffer, error_buffer_size);
    return static_cast<int>(result);
  }
  if (info != nullptr) {
    info->flags = segment.flags;
    info->mode = static_cast<std::uint32_t>(segment.mode);
    info->table_index = segment.table_index;
    info->entry_count = static_cast<std::uint32_t>(segment.entries.size());
    info->function_count = function_count;
  }
  return static_cast<int>(netsec::ErrorCode::ok);
}

int netsec_element_index_is_valid(std::uint32_t function_index,
                                  std::uint32_t function_count) {
  return netsec::element_function_index_is_valid(function_index, function_count)
             ? 1
             : 0;
}

}  // extern "C"
