#pragma once

#include <cstddef>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <vector>

namespace grayrecon {

// 8-bit grayscale image, row-major.
struct Image {
    std::size_t width = 0;
    std::size_t height = 0;
    std::vector<std::uint8_t> data;

    Image() = default;
    Image(std::size_t w, std::size_t h, std::uint8_t fill = 0)
        : width(w), height(h), data(w * h, fill) {}

    std::uint8_t& at(std::size_t r, std::size_t c) { return data[r * width + c]; }
    std::uint8_t at(std::size_t r, std::size_t c) const { return data[r * width + c]; }
};

enum class Connectivity { Four, Eight };

// Thrown when the marker is not pointwise <= the mask.
class MarkerExceedsMaskError : public std::runtime_error {
public:
    MarkerExceedsMaskError(std::size_t row, std::size_t col,
                           std::uint8_t markerValue, std::uint8_t maskValue)
        : std::runtime_error("marker exceeds mask at (row=" + std::to_string(row) +
                             ", col=" + std::to_string(col) +
                             "): marker=" + std::to_string(markerValue) +
                             " > mask=" + std::to_string(maskValue)),
          row(row), col(col), markerValue(markerValue), maskValue(maskValue) {}

    std::size_t row;
    std::size_t col;
    std::uint8_t markerValue;
    std::uint8_t maskValue;
};

struct ReconstructionResult {
    Image image;            // reconstructed image (fixpoint of dilation under mask)
    std::uint64_t updates = 0;    // number of pixel value writes during the algorithm
    std::uint64_t queuePops = 0;  // number of FIFO queue pops during propagation
    bool fixedPoint = false;      // verified: (result dilated, then clipped by mask) == result
};

// Grayscale morphological reconstruction of `marker` under `mask` by dilation.
//
// Uses Vincent's hybrid algorithm: one forward + one backward raster scan for
// initialization, then FIFO queue propagation. No repeated full-image sweeps.
//
// Throws std::invalid_argument on size mismatch and MarkerExceedsMaskError when
// marker(p) > mask(p) for any pixel p.
ReconstructionResult reconstructByDilation(const Image& marker, const Image& mask,
                                           Connectivity conn = Connectivity::Eight);

// Returns true iff `recon` is a fixpoint: min(dilate(recon), mask) == recon.
bool isFixedPoint(const Image& recon, const Image& mask, Connectivity conn);

} // namespace grayrecon
