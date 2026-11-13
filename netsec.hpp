#pragma once

#include <cstddef>
#include <cstdint>
#include <memory>
#include <string>
#include <vector>

namespace netsec {

inline constexpr std::uint64_t kWasmPageSize = 65536;
inline constexpr std::uint64_t kMaxMemory32Pages = 65536;

enum class ValueType : std::uint8_t {
  i32 = 0x7f,
  i64 = 0x7e,
  f32 = 0x7d,
  f64 = 0x7c,
  v128 = 0x7b,
  funcref = 0x70,
  externref = 0x6f,
};

const char* value_type_name(ValueType type) noexcept;

struct Limits {
  std::uint64_t min_pages = 0;
  std::uint64_t max_pages = 0;
  bool has_max = false;
  bool shared = false;
  bool memory64 = false;
};

struct MemoryType {
  Limits limits;
};

struct GlobalType {
  ValueType type = ValueType::i32;
  bool is_mutable = false;
};

struct GlobalValue {
  ValueType type = ValueType::i32;
  std::uint64_t bits = 0;
};

class Memory {
 public:
  Memory() = default;
  explicit Memory(std::uint64_t pages, std::uint8_t fill = 0);

  std::size_t size() const noexcept;
  bool empty() const noexcept;
  std::uint8_t* data() noexcept;
  const std::uint8_t* data() const noexcept;
  std::uint8_t& operator[](std::size_t index) noexcept;
  const std::uint8_t& operator[](std::size_t index) const noexcept;

 private:
  std::shared_ptr<std::vector<std::uint8_t>> bytes_;
};

enum class ConstOp : std::uint8_t {
  i32_const,
  i64_const,
  f32_const,
  f64_const,
  global_get,
  ref_null,
  ref_func,
};

struct ConstInstr {
  ConstOp op = ConstOp::i32_const;
  std::uint64_t value = 0;
  std::uint32_t index = 0;
  ValueType type = ValueType::i32;
};

using ConstExpr = std::vector<ConstInstr>;

struct GlobalDecl {
  GlobalType type;
  ConstExpr init;
};

enum class DataMode : std::uint8_t {
  passive,
  active,
};

struct DataSegment {
  DataMode mode = DataMode::passive;
  std::uint32_t memory_index = 0;
  ConstExpr offset;
  std::vector<std::uint8_t> bytes;
};

struct Module {
  std::vector<MemoryType> imported_memories;
  std::vector<GlobalType> imported_globals;
  std::vector<MemoryType> memories;
  std::vector<GlobalDecl> globals;
  std::vector<DataSegment> data_segments;
  bool has_start = false;
  std::uint32_t start_function = 0;
};

struct DecodeResult {
  std::unique_ptr<Module> module;
  std::string error;
  std::size_t error_offset = 0;

  bool ok() const noexcept { return module != nullptr; }
  explicit operator bool() const noexcept { return ok(); }
};

DecodeResult decode(const std::uint8_t* wasm, std::size_t size);
DecodeResult decode(const std::vector<std::uint8_t>& wasm);

struct Imports {
  std::vector<Memory> memories;
  std::vector<GlobalValue> globals;
};

class Instance {
 public:
  std::size_t memory_count() const noexcept;
  bool has_memory(std::uint32_t index) const noexcept;
  Memory& memory(std::uint32_t index);
  const Memory& memory(std::uint32_t index) const;

  const std::vector<GlobalValue>& globals() const noexcept;
  const std::vector<DataSegment>& data_segments() const noexcept;

 private:
  friend struct InstanceAccess;

  std::vector<Memory> memories_;
  std::vector<GlobalValue> globals_;
  std::vector<DataSegment> data_segments_;
};

struct InstantiateResult {
  std::unique_ptr<Instance> instance;
  std::string error;

  bool ok() const noexcept { return instance != nullptr; }
  explicit operator bool() const noexcept { return ok(); }
};

InstantiateResult instantiate(const Module& module, const Imports& imports = {});
InstantiateResult instantiate(const std::uint8_t* wasm, std::size_t size,
                              const Imports& imports = {});
InstantiateResult instantiate(const std::vector<std::uint8_t>& wasm,
                              const Imports& imports = {});

struct DataApplicationResult {
  bool success = false;
  std::string error;

  bool ok() const noexcept { return success; }
  explicit operator bool() const noexcept { return ok(); }
};

DataApplicationResult apply_active_data_segments(
    const Module& module, const std::vector<GlobalValue>& globals,
    std::vector<Memory>& memories);

bool active_data_segment_in_bounds(std::uint64_t memory_size,
                                   std::uint64_t offset,
                                   std::uint64_t length) noexcept;

}  // namespace netsec

#include "netsec_c.h"
