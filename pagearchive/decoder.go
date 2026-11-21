package pagearchive

import "fmt"

// Reader reconstructs pages from one immutable layout.
type Reader struct {
	layout *Layout
}

// NewReader validates a layout and returns a random-access page reader.
func NewReader(layout *Layout) (*Reader, error) {
	if layout == nil {
		return nil, fmt.Errorf("%w: nil layout", ErrInvalidInput)
	}
	pageCount := len(layout.Representations)
	if pageCount == 0 || pageCount > MaxPages {
		return nil, fmt.Errorf("%w: layout page count must be in [1,%d]", ErrInvalidInput, MaxPages)
	}

	pageSize := 0
	for page, representation := range layout.Representations {
		if representation.Page != page {
			return nil, fmt.Errorf("%w: representation %d has page id %d", ErrInvalidInput, page, representation.Page)
		}
		if representation.MetadataCost < 0 {
			return nil, fmt.Errorf("%w: metadata cost cannot be negative", ErrInvalidInput)
		}
		if !representation.Full {
			if representation.Base < 0 || representation.Base >= pageCount || representation.Base == page {
				return nil, fmt.Errorf("%w: page %d has invalid base", ErrInvalidInput, page)
			}
		}
		if representation.Full {
			if len(representation.Payload) == 0 {
				return nil, fmt.Errorf("%w: full page %d is empty", ErrInvalidInput, page)
			}
			if pageSize == 0 {
				pageSize = len(representation.Payload)
			} else if len(representation.Payload) != pageSize {
				return nil, fmt.Errorf("%w: pages have unequal lengths", ErrInvalidInput)
			}
		}
	}

	state := make([]uint8, pageCount)
	var validateStructure func(int) error
	validateStructure = func(page int) error {
		if state[page] == 1 {
			return fmt.Errorf("%w: dependency graph contains a cycle at page %d", ErrInvalidInput, page)
		}
		if state[page] == 2 {
			return nil
		}
		state[page] = 1
		representation := layout.Representations[page]
		if !representation.Full {
			if err := validateStructure(representation.Base); err != nil {
				return err
			}
		}
		state[page] = 2
		return nil
	}

	for page := range layout.Representations {
		if err := validateStructure(page); err != nil {
			return nil, err
		}
	}
	for page := range layout.Representations {
		decoded, err := readWithoutCache(layout, page)
		if err != nil {
			return nil, err
		}
		if len(decoded) != pageSize {
			return nil, fmt.Errorf("%w: decoded page %d has length %d, want %d", ErrInvalidInput, page, len(decoded), pageSize)
		}
		representation := layout.Representations[page]
		if !representation.Full {
			base, err := readWithoutCache(layout, representation.Base)
			if err != nil {
				return nil, err
			}
			_, err = DecodePatch(base, representation.Payload)
			if err != nil {
				return nil, fmt.Errorf("pagearchive: cannot decode page %d: %w", page, err)
			}
		}
	}
	return &Reader{layout: layout}, nil
}

// Read reconstructs a page without using an LRU cache.
func (r *Reader) Read(page int) ([]byte, error) {
	if page < 0 || page >= len(r.layout.Representations) {
		return nil, fmt.Errorf("%w: unknown page %d", ErrInvalidInput, page)
	}
	return readWithoutCache(r.layout, page)
}

func readWithoutCache(layout *Layout, page int) ([]byte, error) {
	representation := layout.Representations[page]
	if representation.Full {
		return append([]byte(nil), representation.Payload...), nil
	}
	return readRecursive(layout, page, map[int]bool{})
}

func readRecursive(layout *Layout, page int, visiting map[int]bool) ([]byte, error) {
	if visiting[page] {
		return nil, fmt.Errorf("%w: dependency graph contains a cycle at page %d", ErrInvalidInput, page)
	}
	visiting[page] = true
	representation := layout.Representations[page]
	if representation.Full {
		return append([]byte(nil), representation.Payload...), nil
	}
	base, err := readRecursive(layout, representation.Base, visiting)
	if err != nil {
		return nil, err
	}
	decoded, err := DecodePatch(base, representation.Payload)
	if err != nil {
		return nil, fmt.Errorf("pagearchive: cannot decode page %d: %w", page, err)
	}
	delete(visiting, page)
	return decoded, nil
}

// SnapshotCache is an independent LRU cache for one transaction snapshot.
type SnapshotCache struct {
	reader    *Reader
	limit     int
	pages     []int
	contents  map[int][]byte
	readBytes int
}

// NewSnapshotCache creates an empty cache holding at most capacity decoded
// pages. Capacity must be at most three.
func (r *Reader) NewSnapshotCache(capacity int) (*SnapshotCache, error) {
	if capacity < 1 || capacity > MaxCachePages {
		return nil, fmt.Errorf("%w: cache capacity must be in [1,%d]", ErrInvalidInput, MaxCachePages)
	}
	return &SnapshotCache{
		reader:   r,
		limit:    capacity,
		contents: make(map[int][]byte),
	}, nil
}

// Read returns a page and records actual persistent bytes read during misses.
// A hit only moves the page to the most-recently-used position.
func (c *SnapshotCache) Read(page int) ([]byte, error) {
	if page < 0 || page >= len(c.reader.layout.Representations) {
		return nil, fmt.Errorf("%w: unknown page %d", ErrInvalidInput, page)
	}
	if content, ok := c.contents[page]; ok {
		c.touch(page)
		return append([]byte(nil), content...), nil
	}

	if err := c.load(page); err != nil {
		return nil, err
	}
	return append([]byte(nil), c.contents[page]...), nil
}

// ReadBytes returns all persistent bytes charged to this snapshot so far.
func (c *SnapshotCache) ReadBytes() int {
	return c.readBytes
}

// Len returns the number of decoded pages currently retained.
func (c *SnapshotCache) Len() int {
	return len(c.pages)
}

func (c *SnapshotCache) load(page int) error {
	if _, ok := c.contents[page]; ok {
		c.touch(page)
		return nil
	}

	representation := c.reader.layout.Representations[page]
	if !representation.Full {
		if err := c.load(representation.Base); err != nil {
			return err
		}
	}

	var content []byte
	if representation.Full {
		content = append([]byte(nil), representation.Payload...)
	} else {
		base := c.contents[representation.Base]
		if base == nil {
			return fmt.Errorf("%w: base page %d was not retained", ErrInvalidInput, representation.Base)
		}
		var err error
		content, err = DecodePatch(base, representation.Payload)
		if err != nil {
			return fmt.Errorf("pagearchive: cannot decode page %d: %w", page, err)
		}
	}

	c.insert(page, content)
	c.readBytes += representation.PersistentCost()
	return nil
}

func (c *SnapshotCache) touch(page int) {
	for i, cached := range c.pages {
		if cached == page {
			c.pages = append(c.pages[:i], c.pages[i+1:]...)
			c.pages = append(c.pages, page)
			return
		}
	}
}

func (c *SnapshotCache) insert(page int, content []byte) {
	c.touch(page)
	c.pages = append(c.pages, page)
	c.contents[page] = content
	if len(c.pages) > c.limit {
		evicted := c.pages[0]
		c.pages = c.pages[1:]
		delete(c.contents, evicted)
	}
}
