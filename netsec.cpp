#include "netsec.hpp"

#include <cstring>
#include <mutex>
#include <sys/mman.h>

namespace netsec {

namespace {

uint64_t pageIndexOf(const void* p) {
  return reinterpret_cast<uintptr_t>(p) / PartitionAllocator::kPageSize;
}

void* mapAnonymous(size_t size) {
  void* p = mmap(nullptr, size, PROT_READ | PROT_WRITE,
                 MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
  return p == MAP_FAILED ? nullptr : p;
}

}  // namespace

PartitionAllocator::~PartitionAllocator() {
  for (auto& kv : domains_) {
    Domain& dom = kv.second;
    for (auto& cls : dom.slabsByClass)
      for (Slab* s : cls.second) delete s;
    for (auto& lg : dom.larges) munmap(lg.first, lg.second);
  }
  for (auto& ext : extents_) munmap(ext.first, ext.second);
}

size_t PartitionAllocator::sizeClassFor(size_t size) {
  size_t s = (size + 15) & ~size_t(15);
  return s < 16 ? 16 : s;
}

uint32_t PartitionAllocator::createDomain() {
  std::unique_lock<std::shared_mutex> lk(mutex_);
  uint32_t id = nextDomainId_++;
  domains_.emplace(id, Domain{});
  return id;
}

void PartitionAllocator::destroyDomain(uint32_t domain) {
  std::unique_lock<std::shared_mutex> lk(mutex_);
  auto it = domains_.find(domain);
  if (it == domains_.end()) return;
  Domain dom = std::move(it->second);
  domains_.erase(it);

  // Large extents: zero, unregister, unmap.
  for (auto& lg : dom.larges) {
    std::memset(lg.first, 0, lg.second);
    uint64_t first = pageIndexOf(lg.first);
    uint64_t count = lg.second / kPageSize;
    for (uint64_t i = 0; i < count; ++i) pages_.erase(first + i);
    munmap(lg.first, lg.second);
  }

  // Slab pages: zero, unregister, recycle. A recycled page may only be
  // handed to another domain afterwards, with a fresh generation.
  for (uint64_t idx : dom.slabPages) {
    void* base = reinterpret_cast<void*>(idx * kPageSize);
    std::memset(base, 0, kPageSize);
    pages_.erase(idx);
    recycledPages_.push_back(base);
  }
  for (auto& cls : dom.slabsByClass)
    for (Slab* s : cls.second) delete s;
}

void* PartitionAllocator::alloc(uint32_t domain, size_t size) {
  if (size == 0) size = 1;
  std::unique_lock<std::shared_mutex> lk(mutex_);
  auto it = domains_.find(domain);
  if (it == domains_.end()) return nullptr;
  if (size > kMaxSmallSize) return allocLarge(it->second, domain, size);
  return allocSmall(it->second, domain, sizeClassFor(size));
}

void PartitionAllocator::free(void* ptr) {
  if (!ptr) return;
  std::unique_lock<std::shared_mutex> lk(mutex_);
  auto it = pages_.find(pageIndexOf(ptr));
  if (it == pages_.end()) return;
  PageInfo info = it->second;  // copy: freeLargeLocked erases registry nodes
  if (info.large) {
    freeLargeLocked(info);
    return;
  }
  Slab* slab = it->second.slab;
  slab->quarantine.push_back(ptr);
  if (slab->quarantine.size() > kQuarantineCapacity) {
    void* reuse = slab->quarantine.front();
    slab->quarantine.erase(slab->quarantine.begin());
    *reinterpret_cast<void**>(reuse) = slab->freeHead;
    slab->freeHead = reuse;
  }
}

PartitionAllocator::OwnerInfo PartitionAllocator::pageOwner(
    const void* ptr) const {
  if (!ptr) return {};
  std::shared_lock<std::shared_mutex> lk(mutex_);
  auto it = pages_.find(pageIndexOf(ptr));
  if (it == pages_.end()) return {};
  return OwnerInfo{it->second.domain, it->second.generation, true};
}

size_t PartitionAllocator::debugRecycledPageCount() const {
  std::shared_lock<std::shared_mutex> lk(mutex_);
  return recycledPages_.size();
}

size_t PartitionAllocator::debugLivePageCount() const {
  std::shared_lock<std::shared_mutex> lk(mutex_);
  return pages_.size();
}

void* PartitionAllocator::allocSmall(Domain& dom, uint32_t domain,
                                     size_t sizeClass) {
  auto& slabs = dom.slabsByClass[sizeClass];
  for (Slab* s : slabs) {
    if (s->freeHead) {
      void* p = s->freeHead;
      s->freeHead = *reinterpret_cast<void**>(p);
      return p;
    }
  }
  Slab* s = newSlab(dom, domain, sizeClass);
  if (!s) return nullptr;
  void* p = s->freeHead;
  s->freeHead = *reinterpret_cast<void**>(p);
  return p;
}

void* PartitionAllocator::allocLarge(Domain& dom, uint32_t domain,
                                     size_t size) {
  size_t extent = (size + kPageSize - 1) / kPageSize * kPageSize;
  void* base = mapAnonymous(extent);
  if (!base) return nullptr;
  uint64_t generation = nextGeneration_++;
  uint64_t first = pageIndexOf(base);
  for (uint64_t i = 0; i < extent / kPageSize; ++i) {
    PageInfo info;
    info.domain = domain;
    info.generation = generation;
    info.large = true;
    info.extentBase = base;
    info.extentSize = extent;
    pages_[first + i] = info;
  }
  dom.larges.emplace_back(base, extent);
  return base;
}

PartitionAllocator::Slab* PartitionAllocator::newSlab(Domain& dom,
                                                      uint32_t domain,
                                                      size_t sizeClass) {
  uint64_t generation = 0;
  void* page = acquirePage(&generation);
  if (!page) return nullptr;

  Slab* slab = new Slab();
  slab->domain = domain;
  slab->blockSize = sizeClass;
  size_t blocks = kPageSize / sizeClass;
  char* base = static_cast<char*>(page);
  for (size_t i = 0; i + 1 < blocks; ++i)
    *reinterpret_cast<void**>(base + i * sizeClass) = base + (i + 1) * sizeClass;
  *reinterpret_cast<void**>(base + (blocks - 1) * sizeClass) = nullptr;
  slab->freeHead = base;

  uint64_t idx = pageIndexOf(page);
  PageInfo info;
  info.domain = domain;
  info.generation = generation;
  info.slab = slab;
  pages_[idx] = info;
  dom.slabPages.insert(idx);
  dom.slabsByClass[sizeClass].push_back(slab);
  return slab;
}

void* PartitionAllocator::acquirePage(uint64_t* generationOut) {
  *generationOut = nextGeneration_++;
  if (!recycledPages_.empty()) {
    void* p = recycledPages_.back();
    recycledPages_.pop_back();
    return p;
  }
  if (extentRemaining_ < kPageSize) {
    void* ext = mapAnonymous(kExtentSize);
    if (!ext) return nullptr;
    extents_.emplace_back(ext, kExtentSize);
    extentCursor_ = static_cast<char*>(ext);
    extentRemaining_ = kExtentSize;
  }
  void* p = extentCursor_;
  extentCursor_ += kPageSize;
  extentRemaining_ -= kPageSize;
  return p;
}

void PartitionAllocator::freeLargeLocked(const PageInfo& info) {
  std::memset(info.extentBase, 0, info.extentSize);
  uint64_t first = pageIndexOf(info.extentBase);
  for (uint64_t i = 0; i < info.extentSize / kPageSize; ++i)
    pages_.erase(first + i);
  auto domIt = domains_.find(info.domain);
  if (domIt != domains_.end()) {
    auto& larges = domIt->second.larges;
    for (auto it = larges.begin(); it != larges.end(); ++it) {
      if (it->first == info.extentBase) {
        larges.erase(it);
        break;
      }
    }
  }
  munmap(info.extentBase, info.extentSize);
}

}  // namespace netsec
