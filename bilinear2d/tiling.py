"""Tiled execution of grid sampling.

The output grid is processed in tiles, but every boundary decision (clamp
range, reflection period, zero-padding mask) uses the *global* image size.
Tile boundaries are purely an execution partition and are never treated as
image boundaries; gradient contributions from all tiles are accumulated into
one shared ``grad_data``.
"""

from .sampling import sample, sample_backward


def _tile_ranges(length, tile):
    ranges = []
    start = 0
    while start < length:
        ranges.append((start, min(start + tile, length)))
        start += tile
    return ranges


def sample_grid_tiled(data, coord_grid, tile_shape, align, boundary,
                      checkpoint=False):
    """Sample ``data`` on a 2D grid of normalized coords, tile by tile.

    ``coord_grid[iy][ix]`` is a ``(u, v)`` pair.  Returns a dict with
    ``values``/``masks`` grids and per-tile records for
    :func:`sample_grid_tiled_backward`.
    """
    height_out = len(coord_grid)
    width_out = len(coord_grid[0])
    tile_h, tile_w = tile_shape
    values = [[0.0] * width_out for _ in range(height_out)]
    masks = [[0.0] * width_out for _ in range(height_out)]
    tiles = []
    for y0, y1 in _tile_ranges(height_out, tile_h):
        for x0, x1 in _tile_ranges(width_out, tile_w):
            positions = [(iy, ix)
                         for iy in range(y0, y1)
                         for ix in range(x0, x1)]
            coords = [coord_grid[iy][ix] for iy, ix in positions]
            # Boundary handling inside sample() always sees the full image
            # dimensions, never the tile extent.
            vals, ms, cache = sample(data, coords, align=align,
                                     boundary=boundary, checkpoint=checkpoint)
            for (iy, ix), value, mask in zip(positions, vals, ms):
                values[iy][ix] = value
                masks[iy][ix] = mask
            tiles.append((positions, cache))
    return {"values": values, "masks": masks, "tiles": tiles}


def sample_grid_tiled_backward(data, grad_grid, forward):
    """Backward of :func:`sample_grid_tiled`.

    Accumulates ``grad_data`` across tiles and returns
    ``(grad_data, grad_coords_grid)``.
    """
    height, width = len(data), len(data[0])
    height_out = len(grad_grid)
    width_out = len(grad_grid[0])
    grad_data = [[0.0] * width for _ in range(height)]
    grad_coords = [[None] * width_out for _ in range(height_out)]
    for positions, cache in forward["tiles"]:
        grad_flat = [grad_grid[iy][ix] for iy, ix in positions]
        tile_grad_data, tile_grad_coords = sample_backward(
            data, grad_flat, cache)
        for iy in range(height):
            for ix in range(width):
                grad_data[iy][ix] += tile_grad_data[iy][ix]
        for (iy, ix), grad_uv in zip(positions, tile_grad_coords):
            grad_coords[iy][ix] = grad_uv
    return grad_data, grad_coords
