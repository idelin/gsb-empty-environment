#include "netsec.hpp"

#include <cstdio>
#include <cstring>
#include <fstream>
#include <string>
#include <vector>

namespace {

bool read_file(const char* path, std::vector<std::uint8_t>& data) {
  std::ifstream file(path, std::ios::binary);
  if (!file) {
    return false;
  }
  data.assign(std::istreambuf_iterator<char>(file),
              std::istreambuf_iterator<char>());
  return true;
}

void usage(const char* program) {
  std::fprintf(stderr,
               "usage: %s [--validate] [--element --function-count N] FILE\n",
               program);
}

void print_error(const netsec::Error& error) {
  std::fprintf(stderr, "%s", netsec::error_code_name(error.code));
  if (!error.message.empty()) {
    std::fprintf(stderr, " at byte %zu: %s", error.offset,
                 error.message.c_str());
  }
  std::fprintf(stderr, "\n");
}

void print_instance(const netsec::Instance& instance) {
  std::printf("ok functions=%u tables=%u\n", instance.function_count(),
              instance.table_count());
  for (std::uint32_t table_index = 0;
       table_index < instance.table_count(); ++table_index) {
    const netsec::TableInstance& table = instance.tables[table_index];
    std::printf("table[%u] length=%zu\n", table_index, table.entries.size());
    for (std::size_t element_index = 0; element_index < table.entries.size();
         ++element_index) {
      const netsec::TableEntry& entry = table.entries[element_index];
      if (entry.is_null) {
        std::printf("  [%zu] = null\n", element_index);
      } else {
        std::printf("  [%zu] = func[%u]\n", element_index,
                    entry.function_index);
      }
    }
  }
}

}  // namespace

int main(int argc, char** argv) {
  bool validate_only = false;
  bool element_mode = false;
  std::uint32_t function_count = 0;
  const char* path = nullptr;

  for (int index = 1; index < argc; ++index) {
    if (std::strcmp(argv[index], "--validate") == 0) {
      validate_only = true;
    } else if (std::strcmp(argv[index], "--element") == 0) {
      element_mode = true;
    } else if (std::strcmp(argv[index], "--function-count") == 0) {
      if (index + 1 >= argc) {
        usage(argv[0]);
        return 2;
      }
      function_count =
          static_cast<std::uint32_t>(std::strtoul(argv[++index], nullptr, 10));
    } else if (path == nullptr) {
      path = argv[index];
    } else {
      usage(argv[0]);
      return 2;
    }
  }
  if (path == nullptr) {
    usage(argv[0]);
    return 2;
  }

  std::vector<std::uint8_t> data;
  if (!read_file(path, data)) {
    std::fprintf(stderr, "cannot read %s\n", path);
    return 2;
  }

  if (element_mode) {
    netsec::ElementSegment segment;
    netsec::Error error;
    const netsec::ErrorCode result =
        netsec::decode_element_segment(data.data(), data.size(), function_count,
                                       segment, error);
    if (result != netsec::ErrorCode::ok) {
      print_error(error);
      return 1;
    }
    std::printf("ok flags=%u mode=%u entries=%zu function_count=%u\n",
                segment.flags, static_cast<unsigned>(segment.mode),
                segment.entries.size(), function_count);
    for (std::size_t index = 0; index < segment.entries.size(); ++index) {
      const netsec::ElementEntry& entry = segment.entries[index];
      if (entry.is_null) {
        std::printf("  [%zu] = null\n", index);
      } else {
        std::printf("  [%zu] = func[%u]\n", index, entry.function_index);
      }
    }
    return 0;
  }

  if (validate_only) {
    netsec::Module module;
    netsec::Error error;
    const netsec::ErrorCode decoded =
        netsec::decode_module(data.data(), data.size(), module, error);
    if (decoded != netsec::ErrorCode::ok) {
      print_error(error);
      return 1;
    }
    const netsec::ErrorCode valid = netsec::validate_module(module, error);
    if (valid != netsec::ErrorCode::ok) {
      print_error(error);
      return 1;
    }
    std::printf("ok functions=%u tables=%u\n", module.function_count(),
                module.table_count());
    return 0;
  }

  netsec::Instance instance;
  netsec::Error error;
  const netsec::ErrorCode result =
      netsec::decode_and_instantiate(data.data(), data.size(), instance, error);
  if (result != netsec::ErrorCode::ok) {
    print_error(error);
    return 1;
  }
  print_instance(instance);
  return 0;
}
