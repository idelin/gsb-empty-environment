#include "grayrecon/reconstruct.hpp"

#include <algorithm>
#include <deque>

namespace grayrecon {

namespace {

// Neighbor offsets (dr, dc) for the two raster passes and full neighborhood.
struct Neighborhood {
    std::vector<std::pair<int, int>> forward;  // reached in forward raster scan
    std::vector<std::pair<int, int>> backward; // reached in backward raster scan
    std::vector<std::pair<int, int>> all;      // full neighborhood
};

Neighborhood makeNeighborhood(Connectivity conn) {
    Neighborhood nb;
    if (conn == Connectivity::Eight) {
        nb.forward = {{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}};
        nb.backward = {{1, 1}, {1, 0}, {1, -1}, {0, 1}};
        nb.all = {{-1, -1}, {-1, 0}, {-1, 1}, {0, -1},
                  {0, 1},   {1, -1}, {1, 0},  {1, 1}};
    } else {
        nb.forward = {{-1, 0}, {0, -1}};
        nb.backward = {{1, 0}, {0, 1}};
        nb.all = {{-1, 0}, {0, -1}, {0, 1}, {1, 0}};
    }
    return nb;
}

template <typename F>
void forEachNeighbor(std::size_t r, std::size_t c, std::size_t h, std::size_t w,
                     const std::vector<std::pair<int, int>>& offsets, F&& fn) {
    for (const auto& [dr, dc] : offsets) {
        const int nr = static_cast<int>(r) + dr;
        const int nc = static_cast<int>(c) + dc;
        if (nr >= 0 && nr < static_cast<int>(h) && nc >= 0 && nc < static_cast<int>(w))
            fn(static_cast<std::size_t>(nr), static_cast<std::size_t>(nc));
    }
}

} // namespace

ReconstructionResult reconstructByDilation(const Image& marker, const Image& mask,
                                           Connectivity conn) {
    if (marker.width != mask.width || marker.height != mask.height)
        throw std::invalid_argument("marker and mask dimensions differ");

    const std::size_t w = marker.width;
    const std::size_t h = marker.height;

    // Precondition: marker must be pointwise <= mask.
    for (std::size_t r = 0; r < h; ++r)
        for (std::size_t c = 0; c < w; ++c)
            if (marker.at(r, c) > mask.at(r, c))
                throw MarkerExceedsMaskError(r, c, marker.at(r, c), mask.at(r, c));

    const Neighborhood nb = makeNeighborhood(conn);

    ReconstructionResult result;
    result.image = marker;
    std::vector<std::uint8_t>& J = result.image.data;

    // Pass 1: forward raster scan. Propagate along already-visited neighbors.
    for (std::size_t r = 0; r < h; ++r) {
        for (std::size_t c = 0; c < w; ++c) {
            const std::size_t i = r * w + c;
            std::uint8_t m = J[i];
            forEachNeighbor(r, c, h, w, nb.forward, [&](std::size_t nr, std::size_t nc) {
                m = std::max(m, J[nr * w + nc]);
            });
            const std::uint8_t v = std::min(m, mask.data[i]);
            if (v != J[i]) {
                J[i] = v;
                ++result.updates;
            }
        }
    }

    // Pass 2: backward (anti-raster) scan.
    for (std::size_t ri = h; ri-- > 0;) {
        for (std::size_t ci = w; ci-- > 0;) {
            const std::size_t i = ri * w + ci;
            std::uint8_t m = J[i];
            forEachNeighbor(ri, ci, h, w, nb.backward, [&](std::size_t nr, std::size_t nc) {
                m = std::max(m, J[nr * w + nc]);
            });
            const std::uint8_t v = std::min(m, mask.data[i]);
            if (v != J[i]) {
                J[i] = v;
                ++result.updates;
            }
        }
    }

    // Queue initialization: pixels having a neighbor that can still be raised.
    std::deque<std::size_t> fifo;
    for (std::size_t r = 0; r < h; ++r) {
        for (std::size_t c = 0; c < w; ++c) {
            const std::size_t i = r * w + c;
            bool needed = false;
            forEachNeighbor(r, c, h, w, nb.all, [&](std::size_t nr, std::size_t nc) {
                const std::size_t j = nr * w + nc;
                if (J[j] < J[i] && J[j] < mask.data[j])
                    needed = true;
            });
            if (needed)
                fifo.push_back(i);
        }
    }

    // FIFO propagation: only touches pixels that can still change.
    while (!fifo.empty()) {
        const std::size_t i = fifo.front();
        fifo.pop_front();
        ++result.queuePops;
        const std::size_t r = i / w;
        const std::size_t c = i % w;
        forEachNeighbor(r, c, h, w, nb.all, [&](std::size_t nr, std::size_t nc) {
            const std::size_t j = nr * w + nc;
            if (J[j] < J[i] && J[j] != mask.data[j]) {
                J[j] = std::min(J[i], mask.data[j]);
                ++result.updates;
                fifo.push_back(j);
            }
        });
    }

    result.fixedPoint = isFixedPoint(result.image, mask, conn);
    return result;
}

bool isFixedPoint(const Image& recon, const Image& mask, Connectivity conn) {
    if (recon.width != mask.width || recon.height != mask.height)
        return false;
    const std::size_t w = recon.width;
    const std::size_t h = recon.height;
    const Neighborhood nb = makeNeighborhood(conn);

    for (std::size_t r = 0; r < h; ++r) {
        for (std::size_t c = 0; c < w; ++c) {
            const std::size_t i = r * w + c;
            std::uint8_t m = recon.data[i];
            forEachNeighbor(r, c, h, w, nb.all, [&](std::size_t nr, std::size_t nc) {
                m = std::max(m, recon.data[nr * w + nc]);
            });
            if (std::min(m, mask.data[i]) != recon.data[i])
                return false;
        }
    }
    return true;
}

} // namespace grayrecon
