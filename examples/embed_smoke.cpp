// Minimal host-embedding smoke test: include only the public header.
#include "secret_transaction.hpp"

#include <cstddef>

int main() {
    netsec::SecretBuffer key(16);
    for (std::size_t i = 0; i < key.size(); ++i) {
        key.write(i, static_cast<std::byte>(i + 1));
    }

    {
        auto window = key.borrow(4, 8);
        window.write(0, std::byte{0x7E});
    }

    netsec::SecretOwner owner = key.commit();
    return owner.size() == 16 && owner.data()[4] == std::byte{0x7E} ? 0 : 1;
}
