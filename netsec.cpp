#include "netsec.hpp"

#include <new>
#include <random>

namespace netsec {

std::uintptr_t protect_ptr(std::uintptr_t pos, std::uintptr_t ptr) noexcept {
    return (pos >> 12) ^ ptr;
}

std::uintptr_t reveal_ptr(std::uintptr_t pos, std::uintptr_t enc) noexcept {
    return (pos >> 12) ^ enc;
}

namespace {

std::uintptr_t make_key() {
    std::random_device rd;
    std::uintptr_t k = (static_cast<std::uintptr_t>(rd()) << 32) ^ rd();
    return k != 0 ? k : 0x9e3779b97f4a7c15ULL;
}

bool aligned_16(std::uintptr_t addr) {
    return (addr & (TcacheAllocator::kAlignment - 1)) == 0;
}

} // namespace

TcacheAllocator::TcacheAllocator(std::size_t chunk_size)
    : chunk_size_(chunk_size < sizeof(FreeChunk) ? sizeof(FreeChunk) : chunk_size),
      key_(make_key()) {
    chunk_size_ = (chunk_size_ + kAlignment - 1) & ~(kAlignment - 1);
}

TcacheAllocator::~TcacheAllocator() {
    for (void* p : os_blocks_)
        ::operator delete(p, chunk_size_, std::align_val_t(kAlignment));
}

[[noreturn]] void TcacheAllocator::fail(const char* reason) const {
    throw SecurityViolation(reason);
}

void* TcacheAllocator::alloc() {
    if (head_ == nullptr) {
        void* p = ::operator new(chunk_size_, std::align_val_t(kAlignment));
        os_blocks_.push_back(p);
        return p;
    }
    FreeChunk* victim = head_;
    const std::uintptr_t vpos = reinterpret_cast<std::uintptr_t>(victim);
    if (!aligned_16(vpos))
        fail("malloc(): unaligned tcache chunk detected");
    const std::uintptr_t next = reveal_ptr(vpos, victim->next);
    if (next != 0 && !aligned_16(next))
        fail("malloc(): unaligned tcache chunk detected");
    head_ = reinterpret_cast<FreeChunk*>(next);
    victim->key = 0;
    --count_;
    return victim;
}

void TcacheAllocator::free(void* p) {
    if (p == nullptr)
        return;
    auto* chunk = static_cast<FreeChunk*>(p);
    const std::uintptr_t pos = reinterpret_cast<std::uintptr_t>(chunk);
    if (chunk->key == key_) {
        // 块曾被本分配器释放过：遍历空闲链确认是否重复释放。
        FreeChunk* cur = head_;
        for (std::size_t i = 0; cur != nullptr && i < count_; ++i) {
            if (cur == chunk)
                fail("double free detected in tcache");
            cur = reinterpret_cast<FreeChunk*>(
                reveal_ptr(reinterpret_cast<std::uintptr_t>(cur), cur->next));
        }
    }
    chunk->next = protect_ptr(pos, reinterpret_cast<std::uintptr_t>(head_));
    chunk->key = key_;
    head_ = chunk;
    ++count_;
}

void TcacheAllocator::write_next(void* chunk, void* target) noexcept {
    auto* c = static_cast<FreeChunk*>(chunk);
    c->next = protect_ptr(reinterpret_cast<std::uintptr_t>(c),
                          reinterpret_cast<std::uintptr_t>(target));
}

} // namespace netsec
