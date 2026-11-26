#pragma once

#include "machosec/machosec.h"

#include <cstdint>
#include <map>
#include <string>
#include <vector>

namespace machosec::detail {

struct EntValue {
    enum class Kind {
        Null,
        Bool,
        Integer,
        String,
        Bytes,
        Array,
        Dict,
    };

    Kind kind = Kind::Null;
    bool boolean = false;
    int64_t integer = 0;
    std::string text;
    std::vector<uint8_t> bytes;
    std::vector<EntValue> array;
    std::map<std::string, EntValue> dict;
};

[[noreturn]] void fail(const std::string& message);

EntValue parse_xml_entitlements(ByteView bytes);
EntValue parse_der_entitlements(ByteView bytes);

bool semantic_entitlements_equal(const EntValue& xml,
                                 const EntValue& der,
                                 bool only_sensitive,
                                 std::string* difference);

}  // namespace machosec::detail
