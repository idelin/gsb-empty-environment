#pragma once

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace netsec {

enum class ErrorCode : int {
  ok = 0,
  invalid_magic = 1,
  invalid_version = 2,
  malformed = 3,
  unsupported_feature = 4,
  type_index_out_of_bounds = 5,
  function_index_out_of_bounds = 6,
  table_index_out_of_bounds = 7,
  table_offset_out_of_bounds = 8,
  element_function_index_out_of_bounds = 9,
  element_type_mismatch = 10,
  resource_limit = 11,
  internal_error = 12
};

struct Error {
  ErrorCode code = ErrorCode::ok;
  std::size_t offset = 0;
  std::string message;

  explicit operator bool() const { return code != ErrorCode::ok; }
};

enum class RefKind : std::uint8_t {
  funcref = 0x70,
  externref = 0x6f
};

struct Limits {
  std::uint32_t min = 0;
  std::uint32_t max = 0;
  bool has_max = false;
};

struct TableType {
  RefKind element_type = RefKind::funcref;
  Limits limits;
};

struct GlobalType {
  std::uint8_t value_type = 0;
  bool mutable_ = false;
};

struct FunctionType {
  std::vector<std::uint8_t> params;
  std::vector<std::uint8_t> results;
};

struct ConstExpr {
  enum class Kind {
    i32_const,
    i64_const,
    f32_const,
    f64_const,
    global_get,
    ref_null,
    ref_func
  };

  Kind kind = Kind::i32_const;
  std::int64_t integer = 0;
  std::uint64_t bits = 0;
  std::uint32_t index = 0;
  RefKind ref_kind = RefKind::funcref;
  std::uint32_t instruction_count = 0;
  std::size_t offset = 0;
};

struct ElementEntry {
  bool is_null = false;
  std::uint32_t function_index = 0;
};

struct ElementSegment {
  enum class Mode {
    active,
    passive,
    declarative
  };

  std::uint32_t flags = 0;
  Mode mode = Mode::active;
  std::uint32_t table_index = 0;
  RefKind element_type = RefKind::funcref;
  ConstExpr offset;
  std::vector<ElementEntry> entries;
  std::size_t offset_in_binary = 0;
};

struct Export {
  enum class Kind : std::uint8_t {
    function = 0,
    table = 1,
    memory = 2,
    global = 3
  };

  std::string name;
  Kind kind = Kind::function;
  std::uint32_t index = 0;
};

struct Module {
  std::vector<FunctionType> types;
  // Function index space: imported functions first, then defined functions.
  std::vector<std::uint32_t> function_type_indices;
  std::uint32_t imported_function_count = 0;
  // Table index space: imported tables first, then defined tables.
  std::vector<TableType> tables;
  std::uint32_t imported_table_count = 0;
  std::uint32_t imported_memory_count = 0;
  std::uint32_t defined_memory_count = 0;
  std::vector<GlobalType> globals;
  std::vector<ElementSegment> elements;
  std::vector<Export> exports;
  bool has_start = false;
  std::uint32_t start_function = 0;

  std::uint32_t function_count() const {
    return static_cast<std::uint32_t>(function_type_indices.size());
  }

  std::uint32_t table_count() const {
    return static_cast<std::uint32_t>(tables.size());
  }
};

struct TableEntry {
  bool is_null = true;
  std::uint32_t function_index = 0;
};

struct TableInstance {
  TableType type;
  std::vector<TableEntry> entries;
};

struct Instance {
  std::vector<std::uint32_t> function_type_indices;
  std::vector<TableInstance> tables;
  std::vector<GlobalType> globals;

  std::uint32_t function_count() const {
    return static_cast<std::uint32_t>(function_type_indices.size());
  }

  std::uint32_t table_count() const {
    return static_cast<std::uint32_t>(tables.size());
  }
};

const char* error_code_name(ErrorCode code);

// Decodes a binary module without instantiating it.
ErrorCode decode_module(const std::uint8_t* data, std::size_t size, Module& out,
                        Error& error);
ErrorCode decode_module(const std::vector<std::uint8_t>& data, Module& out,
                        Error& error);

ErrorCode decode_and_validate(const std::uint8_t* data, std::size_t size,
                              Module& out, Error& error);
ErrorCode decode_and_validate(const std::vector<std::uint8_t>& data,
                              Module& out, Error& error);

// Validates the decoded module, including every element-segment function
// index. An index is valid iff it is strictly less than function_count().
ErrorCode validate_module(const Module& module, Error& error);

// Allocates tables at their declared minimum sizes and applies active element
// segments. Any out-of-bounds function index causes instantiation to fail.
ErrorCode instantiate_module(const Module& module, Instance& out, Error& error);

ErrorCode decode_and_instantiate(const std::uint8_t* data, std::size_t size,
                                 Instance& out, Error& error);
ErrorCode decode_and_instantiate(const std::vector<std::uint8_t>& data,
                                 Instance& out, Error& error);

// Decodes exactly one element segment. The supplied function_count is the size
// of the module's function index space (imports included). This performs the
// same strict function-index check used during instantiation.
ErrorCode decode_element_segment(const std::uint8_t* data, std::size_t size,
                                 std::uint32_t function_count,
                                 ElementSegment& out, Error& error);

bool element_function_index_is_valid(std::uint32_t function_index,
                                     std::uint32_t function_count);

}  // namespace netsec

extern "C" {

// Stable C status values. Zero is success; every failure is nonzero.
enum NetsecStatus {
  NETSEC_OK = 0,
  NETSEC_ERR_INVALID_MAGIC = 1,
  NETSEC_ERR_INVALID_VERSION = 2,
  NETSEC_ERR_MALFORMED = 3,
  NETSEC_ERR_UNSUPPORTED_FEATURE = 4,
  NETSEC_ERR_TYPE_INDEX_OUT_OF_BOUNDS = 5,
  NETSEC_ERR_FUNCTION_INDEX_OUT_OF_BOUNDS = 6,
  NETSEC_ERR_TABLE_INDEX_OUT_OF_BOUNDS = 7,
  NETSEC_ERR_TABLE_OFFSET_OUT_OF_BOUNDS = 8,
  NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS = 9,
  NETSEC_ERR_ELEMENT_TYPE_MISMATCH = 10,
  NETSEC_ERR_RESOURCE_LIMIT = 11,
  NETSEC_ERR_INTERNAL = 12
};

typedef struct NetsecInstance NetsecInstance;

typedef struct NetsecElementInfo {
  std::uint32_t flags;
  std::uint32_t mode;  // 0 = active, 1 = passive, 2 = declarative
  std::uint32_t table_index;
  std::uint32_t entry_count;
  std::uint32_t function_count;
} NetsecElementInfo;

const char* netsec_error_code_name(int code);

// Parse and validate. In particular, an element-segment function index equal
// to or greater than the function count returns
// NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS.
int netsec_decode_module(const std::uint8_t* data, std::size_t size,
                         char* error_buffer, std::size_t error_buffer_size);

// Parse-only structural check; does not run element/table validation.
int netsec_parse_module(const std::uint8_t* data, std::size_t size,
                        char* error_buffer, std::size_t error_buffer_size);

// Explicit validate entry point (equivalent to netsec_decode_module for
// callers that prefer the validator name).
int netsec_validate_module(const std::uint8_t* data, std::size_t size,
                           char* error_buffer, std::size_t error_buffer_size);

// Parse, validate and instantiate. Returns NULL on failure.
NetsecInstance* netsec_instantiate_module(const std::uint8_t* data,
                                          std::size_t size,
                                          char* error_buffer,
                                          std::size_t error_buffer_size);

// Parse, validate and instantiate into an existing instance. Returns
// NETSEC_OK on success.
int netsec_instantiate_module_into(const std::uint8_t* data, std::size_t size,
                                   NetsecInstance* instance,
                                   char* error_buffer,
                                   std::size_t error_buffer_size);

NetsecInstance* netsec_instance_create(void);
void netsec_instance_destroy(NetsecInstance* instance);
void netsec_instance_free(NetsecInstance* instance);

std::uint32_t netsec_instance_function_count(const NetsecInstance* instance);
std::uint32_t netsec_instance_table_count(const NetsecInstance* instance);
std::uint32_t netsec_instance_table_length(const NetsecInstance* instance,
                                           std::uint32_t table_index);

// Returns 1 if the table entry is a function reference, 0 if it is null, and
// -1 on an invalid argument or instance. On success *function_index receives
// the referenced function index (0 for a null entry).
int netsec_instance_table_entry(const NetsecInstance* instance,
                                std::uint32_t table_index,
                                std::uint32_t element_index,
                                std::uint32_t* function_index);

// Adjudicates one raw element segment against an explicit function count.
// info may be NULL. An index equal to function_count is out of bounds.
int netsec_decode_element_segment(const std::uint8_t* data, std::size_t size,
                                  std::uint32_t function_count,
                                  NetsecElementInfo* info,
                                  char* error_buffer,
                                  std::size_t error_buffer_size);

// Convenience predicate: returns 1 iff function_index < function_count.
int netsec_element_index_is_valid(std::uint32_t function_index,
                                  std::uint32_t function_count);

}  // extern "C"
