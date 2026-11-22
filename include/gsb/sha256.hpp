#pragma once

#include <cstdint>
#include <string>
#include <string_view>

namespace gsb {

std::string sha256_hex(std::string_view data);

}  // namespace gsb
