#include "netsec.hpp"

#include <algorithm>
#include <cstring>
#include <exception>
#include <limits>
#include <new>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

class DecodeFailure : public std::exception {
 public:
  DecodeFailure(std::size_t offset, std::string message)
      : offset_(offset), message_(std::move(message)) {}

  const char* what() const noexcept override { return message_.c_str(); }
  std::size_t offset() const noexcept { return offset_; }

 private:
  std::size_t offset_;
  std::string message_;
};

class Reader {
 public:
  Reader(const std::uint8_t* data, std::size_t size)
      : begin_(data), current_(data), end_(data + size) {}

  bool eof() const noexcept { return current_ == end_; }
  std::size_t offset() const noexcept {
    return static_cast<std::size_t>(current_ - begin_);
  }
  std::size_t remaining() const noexcept {
    return static_cast<std::size_t>(end_ - current_);
  }

  std::uint8_t u8() {
    if (current_ == end_) {
      fail("unexpected end of input");
    }
    return *current_++;
  }

  std::uint32_t u32() {
    return static_cast<std::uint32_t>(uleb(32));
  }

  std::uint64_t u64() { return uleb(64); }

  std::uint64_t uleb(unsigned bits) {
    std::uint64_t result = 0;
    unsigned shift = 0;
    const unsigned max_bytes = (bits + 6) / 7;

    for (unsigned i = 0; i < max_bytes; ++i) {
      const std::uint8_t byte = u8();
      const std::uint64_t payload = byte & 0x7f;

      if (shift >= 64) {
        fail("integer representation too long");
      }
      if (shift + 7 > bits) {
        const unsigned used_bits = bits - shift;
        if (used_bits < 7 && (payload >> used_bits) != 0) {
          fail("integer too large");
        }
      }

      result |= payload << shift;
      if ((byte & 0x80) == 0) {
        return result;
      }
      shift += 7;
    }

    fail("integer representation too long");
  }

  std::int64_t sleb(unsigned bits) {
    std::uint64_t result = 0;
    unsigned shift = 0;
    const unsigned max_bytes = (bits + 6) / 7;

    for (unsigned i = 0; i < max_bytes; ++i) {
      const std::uint8_t byte = u8();
      result |= static_cast<std::uint64_t>(byte & 0x7f) << shift;
      shift += 7;
      if ((byte & 0x80) == 0) {
        if (shift < 64 && (byte & 0x40) != 0) {
          result |= (~std::uint64_t{0}) << shift;
        } else if (shift < 64) {
          result &= (std::uint64_t{1} << shift) - 1;
        }
        return static_cast<std::int64_t>(result);
      }
    }

    fail("integer representation too long");
  }

  std::vector<std::uint8_t> bytes(std::size_t count) {
    if (count > remaining()) {
      fail("byte vector extends past end of section");
    }
    std::vector<std::uint8_t> result(current_, current_ + count);
    current_ += count;
    return result;
  }

  void skip(std::size_t count) {
    if (count > remaining()) {
      fail("section extends past end of input");
    }
    current_ += count;
  }

  Reader subreader(std::size_t count) {
    if (count > remaining()) {
      fail("section extends past end of input");
    }
    Reader result(current_, count);
    current_ += count;
    return result;
  }

  std::string name() {
    const std::uint32_t length = u32();
    if (length > remaining()) {
      fail("name extends past end of section");
    }
    std::string result(reinterpret_cast<const char*>(current_), length);
    current_ += length;
    return result;
  }

  std::uint32_t le32() {
    if (remaining() < 4) {
      fail("unexpected end of input");
    }
    std::uint32_t value = static_cast<std::uint32_t>(current_[0]) |
                          (static_cast<std::uint32_t>(current_[1]) << 8) |
                          (static_cast<std::uint32_t>(current_[2]) << 16) |
                          (static_cast<std::uint32_t>(current_[3]) << 24);
    current_ += 4;
    return value;
  }

  std::uint64_t le64() {
    if (remaining() < 8) {
      fail("unexpected end of input");
    }
    std::uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) {
      value |= static_cast<std::uint64_t>(current_[i]) << (8 * i);
    }
    current_ += 8;
    return value;
  }

  [[noreturn]] void fail(const std::string& message) const {
    throw DecodeFailure(offset(), message);
  }

 private:
  const std::uint8_t* begin_;
  const std::uint8_t* current_;
  const std::uint8_t* end_;
};

ValueType parse_value_type(Reader& reader) {
  switch (reader.u8()) {
    case 0x7f:
      return ValueType::i32;
    case 0x7e:
      return ValueType::i64;
    case 0x7d:
      return ValueType::f32;
    case 0x7c:
      return ValueType::f64;
    case 0x7b:
      return ValueType::v128;
    case 0x70:
      return ValueType::funcref;
    case 0x6f:
      return ValueType::externref;
    default:
      reader.fail("unknown value type");
  }
}

Limits parse_limits(Reader& reader, bool is_memory) {
  const std::uint8_t flags = reader.u8();
  const std::uint8_t allowed_flags = is_memory ? 0x07 : 0x01;
  if ((flags & static_cast<std::uint8_t>(~allowed_flags)) != 0) {
    reader.fail("unsupported limits flags");
  }

  Limits limits;
  limits.has_max = (flags & 0x01) != 0;
  limits.shared = (flags & 0x02) != 0;
  limits.memory64 = (flags & 0x04) != 0;

  limits.min_pages = limits.memory64 ? reader.u64() : reader.u32();
  if (limits.has_max) {
    limits.max_pages = limits.memory64 ? reader.u64() : reader.u32();
    if (limits.max_pages < limits.min_pages) {
      reader.fail("limits maximum is smaller than minimum");
    }
  }

  if (is_memory) {
    if (!limits.memory64 && limits.min_pages > kMaxMemory32Pages) {
      reader.fail("initial memory exceeds 32-bit address space");
    }
    if (limits.has_max && !limits.memory64 &&
        limits.max_pages > kMaxMemory32Pages) {
      reader.fail("maximum memory exceeds 32-bit address space");
    }
    if (limits.shared && !limits.has_max) {
      reader.fail("shared memory requires a maximum");
    }
  } else if (limits.shared || limits.memory64) {
    reader.fail("invalid table limits");
  }

  return limits;
}

MemoryType parse_memory_type(Reader& reader) {
  MemoryType type;
  type.limits = parse_limits(reader, true);
  return type;
}

void parse_table_type(Reader& reader) {
  const std::uint8_t element_type = reader.u8();
  if (element_type != 0x70 && element_type != 0x6f) {
    reader.fail("unknown table element type");
  }
  (void)parse_limits(reader, false);
}

GlobalType parse_global_type(Reader& reader) {
  GlobalType type;
  type.type = parse_value_type(reader);
  const std::uint8_t mutability = reader.u8();
  if (mutability > 1) {
    reader.fail("invalid global mutability");
  }
  type.is_mutable = mutability != 0;
  return type;
}

ConstExpr parse_const_expr(Reader& reader) {
  ConstExpr expression;

  for (;;) {
    const std::uint8_t opcode = reader.u8();
    if (opcode == 0x0b) {
      break;
    }

    ConstInstr instruction;
    switch (opcode) {
      case 0x41: {
        const std::int32_t value = static_cast<std::int32_t>(reader.sleb(32));
        instruction.op = ConstOp::i32_const;
        instruction.value = static_cast<std::uint32_t>(value);
        instruction.type = ValueType::i32;
        break;
      }
      case 0x42:
        instruction.op = ConstOp::i64_const;
        instruction.value = static_cast<std::uint64_t>(reader.sleb(64));
        instruction.type = ValueType::i64;
        break;
      case 0x43:
        instruction.op = ConstOp::f32_const;
        instruction.value = reader.le32();
        instruction.type = ValueType::f32;
        break;
      case 0x44:
        instruction.op = ConstOp::f64_const;
        instruction.value = reader.le64();
        instruction.type = ValueType::f64;
        break;
      case 0x23:
        instruction.op = ConstOp::global_get;
        instruction.index = reader.u32();
        break;
      case 0xd0: {
        const std::uint8_t heap_type = reader.u8();
        instruction.op = ConstOp::ref_null;
        instruction.type =
            heap_type == 0x6f ? ValueType::externref : ValueType::funcref;
        break;
      }
      case 0xd2:
        instruction.op = ConstOp::ref_func;
        instruction.index = reader.u32();
        instruction.type = ValueType::funcref;
        break;
      default:
        reader.fail("unsupported constant expression instruction");
    }
    expression.push_back(instruction);
  }

  return expression;
}

void parse_imports(Reader& section, Module& module) {
  const std::uint32_t count = section.u32();
  if (count > section.remaining()) {
    section.fail("import count exceeds section size");
  }

  for (std::uint32_t i = 0; i < count; ++i) {
    (void)section.name();
    (void)section.name();

    switch (section.u8()) {
      case 0x00:
        (void)section.u32();
        break;
      case 0x01:
        parse_table_type(section);
        break;
      case 0x02:
        module.imported_memories.push_back(parse_memory_type(section));
        break;
      case 0x03:
        module.imported_globals.push_back(parse_global_type(section));
        break;
      default:
        section.fail("unknown import kind");
    }
  }
}

void parse_memories(Reader& section, Module& module) {
  const std::uint32_t count = section.u32();
  if (count > section.remaining()) {
    section.fail("memory count exceeds section size");
  }

  for (std::uint32_t i = 0; i < count; ++i) {
    module.memories.push_back(parse_memory_type(section));
  }
}

void parse_globals(Reader& section, Module& module) {
  const std::uint32_t count = section.u32();
  if (count > section.remaining()) {
    section.fail("global count exceeds section size");
  }

  for (std::uint32_t i = 0; i < count; ++i) {
    GlobalDecl declaration;
    declaration.type = parse_global_type(section);
    declaration.init = parse_const_expr(section);
    module.globals.push_back(std::move(declaration));
  }
}

void parse_data(Reader& section, Module& module) {
  const std::uint32_t count = section.u32();
  if (count > section.remaining()) {
    section.fail("data segment count exceeds section size");
  }

  for (std::uint32_t i = 0; i < count; ++i) {
    const std::uint32_t flags = section.u32();
    DataSegment segment;

    if (flags == 0) {
      segment.mode = DataMode::active;
      segment.memory_index = 0;
      segment.offset = parse_const_expr(section);
    } else if (flags == 1) {
      segment.mode = DataMode::passive;
    } else if (flags == 2) {
      segment.mode = DataMode::active;
      segment.memory_index = section.u32();
      segment.offset = parse_const_expr(section);
    } else {
      section.fail("unsupported data segment flags");
    }

    segment.bytes = section.bytes(section.u32());
    module.data_segments.push_back(std::move(segment));
  }
}

struct EvalValue {
  ValueType type = ValueType::i32;
  std::uint64_t bits = 0;
};

bool evaluate_const_expr(const ConstExpr& expression,
                         const std::vector<GlobalValue>& globals,
                         EvalValue& result, std::string& error) {
  std::vector<EvalValue> stack;
  stack.reserve(expression.size());

  for (const ConstInstr& instruction : expression) {
    switch (instruction.op) {
      case ConstOp::i32_const:
        stack.push_back({ValueType::i32, instruction.value & 0xffffffffu});
        break;
      case ConstOp::i64_const:
        stack.push_back({ValueType::i64, instruction.value});
        break;
      case ConstOp::f32_const:
        stack.push_back({ValueType::f32, instruction.value & 0xffffffffu});
        break;
      case ConstOp::f64_const:
        stack.push_back({ValueType::f64, instruction.value});
        break;
      case ConstOp::global_get:
        if (instruction.index >= globals.size()) {
          error = "constant expression references an unknown global";
          return false;
        }
        stack.push_back({globals[instruction.index].type,
                         globals[instruction.index].bits});
        break;
      case ConstOp::ref_null:
      case ConstOp::ref_func:
        stack.push_back({instruction.type, 0});
        break;
    }
  }

  if (stack.size() != 1) {
    error = "constant expression does not produce exactly one value";
    return false;
  }

  result = stack.back();
  return true;
}

bool memory_size_matches(const Memory& memory, const MemoryType& type,
                         std::string& error) {
  if (memory.size() % kWasmPageSize != 0) {
    error = "imported memory size is not a whole number of pages";
    return false;
  }

  const std::uint64_t pages = memory.size() / kWasmPageSize;
  if (pages < type.limits.min_pages) {
    error = "imported memory is smaller than the declared minimum";
    return false;
  }
  if (type.limits.has_max && pages > type.limits.max_pages) {
    error = "imported memory is larger than the declared maximum";
    return false;
  }
  return true;
}

InstantiateResult instantiate_failure(std::string error) {
  InstantiateResult result;
  result.error = std::move(error);
  return result;
}

}  // namespace

struct InstanceAccess {
  static std::unique_ptr<Instance> create(
      std::vector<Memory> memories, std::vector<GlobalValue> globals,
      std::vector<DataSegment> data_segments) {
    auto instance = std::make_unique<Instance>();
    instance->memories_ = std::move(memories);
    instance->globals_ = std::move(globals);
    instance->data_segments_ = std::move(data_segments);
    return instance;
  }
};

const char* value_type_name(ValueType type) noexcept {
  switch (type) {
    case ValueType::i32:
      return "i32";
    case ValueType::i64:
      return "i64";
    case ValueType::f32:
      return "f32";
    case ValueType::f64:
      return "f64";
    case ValueType::v128:
      return "v128";
    case ValueType::funcref:
      return "funcref";
    case ValueType::externref:
      return "externref";
  }
  return "unknown";
}

Memory::Memory(std::uint64_t pages, std::uint8_t fill)
    : bytes_(std::make_shared<std::vector<std::uint8_t>>()) {
  if (pages > std::numeric_limits<std::size_t>::max() / kWasmPageSize) {
    throw std::length_error("memory size overflows address space");
  }
  bytes_->assign(static_cast<std::size_t>(pages * kWasmPageSize), fill);
}

std::size_t Memory::size() const noexcept {
  return bytes_ == nullptr ? 0 : bytes_->size();
}
bool Memory::empty() const noexcept {
  return bytes_ == nullptr || bytes_->empty();
}
std::uint8_t* Memory::data() noexcept {
  return bytes_ == nullptr ? nullptr : bytes_->data();
}
const std::uint8_t* Memory::data() const noexcept {
  return bytes_ == nullptr ? nullptr : bytes_->data();
}

std::uint8_t& Memory::operator[](std::size_t index) noexcept {
  return (*bytes_)[index];
}

const std::uint8_t& Memory::operator[](std::size_t index) const noexcept {
  return (*bytes_)[index];
}

std::size_t Instance::memory_count() const noexcept { return memories_.size(); }

bool Instance::has_memory(std::uint32_t index) const noexcept {
  return index < memories_.size();
}

Memory& Instance::memory(std::uint32_t index) { return memories_.at(index); }

const Memory& Instance::memory(std::uint32_t index) const {
  return memories_.at(index);
}

const std::vector<GlobalValue>& Instance::globals() const noexcept {
  return globals_;
}

const std::vector<DataSegment>& Instance::data_segments() const noexcept {
  return data_segments_;
}

DecodeResult decode(const std::uint8_t* wasm, std::size_t size) {
  DecodeResult result;

  try {
    if (wasm == nullptr) {
      result.error = "wasm input is null";
      return result;
    }
    if (size < 8) {
      result.error = "wasm input is shorter than the module header";
      return result;
    }

    static constexpr std::uint8_t kMagic[4] = {0x00, 0x61, 0x73, 0x6d};
    static constexpr std::uint8_t kVersion[4] = {0x01, 0x00, 0x00, 0x00};
    if (std::memcmp(wasm, kMagic, sizeof(kMagic)) != 0) {
      result.error = "invalid wasm magic";
      return result;
    }
    if (std::memcmp(wasm + 4, kVersion, sizeof(kVersion)) != 0) {
      result.error = "unsupported wasm version";
      return result;
    }

    Reader reader(wasm + 8, size - 8);
    auto module = std::make_unique<Module>();
    bool seen[13] = {};
    bool has_data_count = false;
    std::uint32_t data_count = 0;

    while (!reader.eof()) {
      const std::uint8_t section_id = reader.u8();
      const std::uint32_t section_size = reader.u32();
      Reader section = reader.subreader(section_size);

      if (section_id > 12) {
        reader.fail("unknown section id");
      }
      if (section_id != 0) {
        if (seen[section_id]) {
          reader.fail("duplicate section");
        }
        seen[section_id] = true;
      }

      switch (section_id) {
        case 0:
          section.skip(section.remaining());
          break;
        case 1:
        case 3:
        case 4:
        case 7:
        case 9:
        case 10:
          section.skip(section.remaining());
          break;
        case 2:
          parse_imports(section, *module);
          break;
        case 5:
          parse_memories(section, *module);
          break;
        case 6:
          parse_globals(section, *module);
          break;
        case 8:
          module->has_start = true;
          module->start_function = section.u32();
          break;
        case 11:
          parse_data(section, *module);
          break;
        case 12:
          has_data_count = true;
          data_count = section.u32();
          break;
      }

      if (!section.eof()) {
        reader.fail("section payload was not fully consumed");
      }
    }

    if (has_data_count && data_count != module->data_segments.size()) {
      result.error = "data count section does not match the data section";
      return result;
    }

    result.module = std::move(module);
    return result;
  } catch (const DecodeFailure& failure) {
    result.error = failure.what();
    result.error_offset = failure.offset();
    return result;
  }
}

DecodeResult decode(const std::vector<std::uint8_t>& wasm) {
  return decode(wasm.data(), wasm.size());
}

bool active_data_segment_in_bounds(std::uint64_t memory_size,
                                   std::uint64_t offset,
                                   std::uint64_t length) noexcept {
  return offset <= memory_size && length <= memory_size - offset;
}

DataApplicationResult apply_active_data_segments(
    const Module& module, const std::vector<GlobalValue>& globals,
    std::vector<Memory>& memories) {
  struct PendingSegment {
    std::uint32_t memory_index = 0;
    std::uint64_t offset = 0;
    const std::vector<std::uint8_t>* bytes = nullptr;
  };

  std::vector<PendingSegment> pending;
  pending.reserve(module.data_segments.size());

  for (std::size_t i = 0; i < module.data_segments.size(); ++i) {
    const DataSegment& segment = module.data_segments[i];
    if (segment.mode != DataMode::active) {
      continue;
    }

    const std::size_t module_memory_count =
        module.imported_memories.size() + module.memories.size();
    if (segment.memory_index >= module_memory_count ||
        segment.memory_index >= memories.size()) {
      return {false, "data segment " + std::to_string(i) +
                         " references an unknown memory"};
    }

    EvalValue offset_value;
    std::string error;
    if (!evaluate_const_expr(segment.offset, globals, offset_value, error)) {
      return {false, "data segment " + std::to_string(i) + ": " + error};
    }

    bool is_memory64 = false;
    if (segment.memory_index < module.imported_memories.size()) {
      is_memory64 =
          module.imported_memories[segment.memory_index].limits.memory64;
    } else {
      const std::size_t defined_index =
          segment.memory_index - module.imported_memories.size();
      is_memory64 = module.memories[defined_index].limits.memory64;
    }

    const ValueType expected_offset_type =
        is_memory64 ? ValueType::i64 : ValueType::i32;
    if (offset_value.type != expected_offset_type) {
      return {false, "data segment " + std::to_string(i) +
                         ": offset expression has type " +
                         value_type_name(offset_value.type) + " but memory " +
                         std::to_string(segment.memory_index) + " requires " +
                         value_type_name(expected_offset_type)};
    }

    const std::uint64_t offset =
        is_memory64 ? offset_value.bits : (offset_value.bits & 0xffffffffu);
    const std::uint64_t memory_size = memories[segment.memory_index].size();

    if (!active_data_segment_in_bounds(memory_size, offset,
                                       segment.bytes.size())) {
      return {false, "data segment " + std::to_string(i) +
                         " is out of bounds: offset " + std::to_string(offset) +
                         " + length " + std::to_string(segment.bytes.size()) +
                         " exceeds memory " +
                         std::to_string(segment.memory_index) + " size " +
                         std::to_string(memory_size)};
    }

    pending.push_back({segment.memory_index, offset, &segment.bytes});
  }

  for (const PendingSegment& segment : pending) {
    if (segment.bytes->empty()) {
      continue;
    }
    std::memcpy(memories[segment.memory_index].data() +
                    static_cast<std::size_t>(segment.offset),
                segment.bytes->data(), segment.bytes->size());
  }

  return {true, {}};
}

InstantiateResult instantiate(const Module& module, const Imports& imports) {
  try {
    if (imports.memories.size() != module.imported_memories.size()) {
      return instantiate_failure(
          "module requires " +
          std::to_string(module.imported_memories.size()) +
          " imported memories but " + std::to_string(imports.memories.size()) +
          " were supplied");
    }
    if (imports.globals.size() != module.imported_globals.size()) {
      return instantiate_failure(
          "module requires " + std::to_string(module.imported_globals.size()) +
          " imported globals but " + std::to_string(imports.globals.size()) +
          " were supplied");
    }

    std::vector<Memory> memories;
    memories.reserve(imports.memories.size() + module.memories.size());

    for (std::size_t i = 0; i < imports.memories.size(); ++i) {
      std::string error;
      if (!memory_size_matches(imports.memories[i],
                               module.imported_memories[i], error)) {
        return instantiate_failure("imported memory " + std::to_string(i) +
                                   ": " + error);
      }
      memories.push_back(imports.memories[i]);
    }

    for (const MemoryType& type : module.memories) {
      if (type.limits.min_pages >
          std::numeric_limits<std::size_t>::max() / kWasmPageSize) {
        return instantiate_failure("defined memory is too large");
      }
      memories.emplace_back(type.limits.min_pages);
    }

    std::vector<GlobalValue> globals;
    globals.reserve(imports.globals.size() + module.globals.size());
    for (std::size_t i = 0; i < imports.globals.size(); ++i) {
      if (imports.globals[i].type != module.imported_globals[i].type) {
        return instantiate_failure("imported global " + std::to_string(i) +
                                   " has the wrong type");
      }
      globals.push_back(imports.globals[i]);
    }

    for (std::size_t i = 0; i < module.globals.size(); ++i) {
      const GlobalDecl& declaration = module.globals[i];
      EvalValue value;
      std::string error;
      if (!evaluate_const_expr(declaration.init, globals, value, error)) {
        return instantiate_failure("global " + std::to_string(i) + ": " +
                                   error);
      }
      if (value.type != declaration.type.type) {
        return instantiate_failure("global " + std::to_string(i) +
                                   ": initializer has type " +
                                   value_type_name(value.type) +
                                   " but the global has type " +
                                   value_type_name(declaration.type.type));
      }
      globals.push_back({value.type, value.bits});
    }

    DataApplicationResult applied =
        apply_active_data_segments(module, globals, memories);
    if (!applied) {
      return instantiate_failure(applied.error);
    }

    return {InstanceAccess::create(std::move(memories), std::move(globals),
                                   module.data_segments),
            {}};
  } catch (const std::bad_alloc&) {
    return instantiate_failure("not enough memory to instantiate module");
  } catch (const std::exception& error) {
    return instantiate_failure(std::string("instantiation failed: ") +
                               error.what());
  }
}

InstantiateResult instantiate(const std::uint8_t* wasm, std::size_t size,
                              const Imports& imports) {
  DecodeResult decoded = decode(wasm, size);
  if (!decoded) {
    return instantiate_failure(decoded.error);
  }
  return instantiate(*decoded.module, imports);
}

InstantiateResult instantiate(const std::vector<std::uint8_t>& wasm,
                              const Imports& imports) {
  return instantiate(wasm.data(), wasm.size(), imports);
}

}  // namespace netsec

struct netsec_instance {
  netsec::Instance instance;
};

namespace {

void write_error(char* buffer, std::size_t buffer_size,
                 const std::string& message) {
  if (buffer == nullptr || buffer_size == 0) {
    return;
  }
  const std::size_t length = std::min(buffer_size - 1, message.size());
  std::memcpy(buffer, message.data(), length);
  buffer[length] = '\0';
}

}  // namespace

extern "C" {

netsec_result_code netsec_instantiate(const std::uint8_t* wasm,
                                      std::size_t size,
                                      netsec_instance** out_instance,
                                      char* error_buffer,
                                      std::size_t error_buffer_size) {
  if (out_instance == nullptr || (wasm == nullptr && size != 0)) {
    write_error(error_buffer, error_buffer_size, "invalid argument");
    return NETSEC_INVALID_ARGUMENT;
  }
  *out_instance = nullptr;

  netsec::DecodeResult decoded = netsec::decode(wasm, size);
  if (!decoded) {
    write_error(error_buffer, error_buffer_size, decoded.error);
    return NETSEC_DECODE_ERROR;
  }

  netsec::InstantiateResult instantiated = netsec::instantiate(*decoded.module);
  if (!instantiated) {
    write_error(error_buffer, error_buffer_size, instantiated.error);
    return NETSEC_INSTANTIATION_ERROR;
  }

  netsec_instance* instance =
      new (std::nothrow) netsec_instance{std::move(*instantiated.instance)};
  if (instance == nullptr) {
    write_error(error_buffer, error_buffer_size, "out of memory");
    return NETSEC_INSTANTIATION_ERROR;
  }

  *out_instance = instance;
  return NETSEC_OK;
}

netsec_result_code netsec_decode_and_instantiate(
    const std::uint8_t* wasm, std::size_t size, netsec_instance** out_instance,
    char* error_buffer, std::size_t error_buffer_size) {
  return netsec_instantiate(wasm, size, out_instance, error_buffer,
                            error_buffer_size);
}

void netsec_instance_destroy(netsec_instance* instance) { delete instance; }

std::size_t netsec_instance_memory_count(const netsec_instance* instance) {
  return instance == nullptr ? 0 : instance->instance.memory_count();
}

std::size_t netsec_instance_memory_size(const netsec_instance* instance,
                                        std::uint32_t memory_index) {
  if (instance == nullptr || !instance->instance.has_memory(memory_index)) {
    return 0;
  }
  return instance->instance.memory(memory_index).size();
}

const std::uint8_t* netsec_instance_memory_data(
    const netsec_instance* instance, std::uint32_t memory_index,
    std::size_t* out_size) {
  if (instance == nullptr || !instance->instance.has_memory(memory_index)) {
    if (out_size != nullptr) {
      *out_size = 0;
    }
    return nullptr;
  }
  const netsec::Memory& memory = instance->instance.memory(memory_index);
  if (out_size != nullptr) {
    *out_size = memory.size();
  }
  return memory.data();
}

int netsec_active_data_segment_in_bounds(std::uint64_t memory_size,
                                         std::uint64_t offset,
                                         std::uint64_t length) {
  return netsec::active_data_segment_in_bounds(memory_size, offset, length) ? 1
                                                                          : 0;
}

}  // extern "C"
