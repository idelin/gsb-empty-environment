#include <netsec.hpp>

int main() {
    netsec::SliceBindingManager manager;
    const auto result = manager.register_slice("imported", 1, 1);
    return result ? 0 : 1;
}
