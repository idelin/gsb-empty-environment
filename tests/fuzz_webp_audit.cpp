#include "webp_audit.hpp"

#include <cstdint>
#include <cstdio>
#include <random>
#include <string_view>
#include <vector>

// Feeds thousands of arbitrary (and RIFF-shaped) byte strings to the auditor.
// The invariant under test is memory safety: no out-of-bounds read regardless
// of how malformed the lengths, padding or headers are.
int main() {
    std::mt19937 rng(0x5eed1234u);
    std::uniform_int_distribution<int> byte_dist(0, 255);
    std::uniform_int_distribution<int> len_dist(0, 64);

    constexpr int kIterations = 20000;
    for (int iter = 0; iter < kIterations; ++iter) {
        std::vector<std::uint8_t> buf;

        if ((iter & 1) != 0) {
            // Half the corpus starts with a structurally plausible RIFF head.
            const std::uint32_t declared =
                static_cast<std::uint32_t>(rng() & 0xffffu);
            const char riff[4] = {'R', 'I', 'F', 'F'};
            const char webp[4] = {'W', 'E', 'B', 'P'};
            buf.insert(buf.end(), riff, riff + 4);
            buf.push_back(static_cast<std::uint8_t>(declared & 0xffu));
            buf.push_back(static_cast<std::uint8_t>((declared >> 8) & 0xffu));
            buf.push_back(static_cast<std::uint8_t>((declared >> 16) & 0xffu));
            buf.push_back(static_cast<std::uint8_t>((declared >> 24) & 0xffu));
            buf.insert(buf.end(), webp, webp + 4);
        }

        const int extra = len_dist(rng);
        for (int i = 0; i < extra; ++i) {
            buf.push_back(static_cast<std::uint8_t>(byte_dist(rng)));
        }

        const auto r = webp_audit::audit(buf.data(), buf.size());
        (void)r.ok();

        // Also exercise the string_view overload and null/empty paths.
        const auto r2 = webp_audit::audit(
            std::string_view(reinterpret_cast<const char*>(buf.data()),
                             buf.size()));
        (void)r2.error;
    }

    const auto empty = webp_audit::audit(nullptr, 0);
    if (empty.error != webp_audit::AuditError::kNullInput) {
        return 1;
    }

    std::printf("fuzz: %d iterations completed safely\n", kIterations);
    return 0;
}
