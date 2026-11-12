package taskpkg

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PlainText returns the exact text coordinate system used by TextSpan:
// every block's Text in preorder, including ignored subtrees, concatenated
// without separators.
func PlainText(root *Block) string {
	var out strings.Builder
	var walk func(*Block)
	walk = func(b *Block) {
		if b == nil {
			return
		}
		out.WriteString(b.Text)
		for _, child := range b.Children {
			walk(child)
		}
	}
	walk(root)
	return out.String()
}

type blockInfo struct {
	block *Block

	// Parent in the full tree, including ignored subtrees.
	parent *blockInfo

	// Preorder index and parent among non-ignored blocks.
	activeIndex  int
	activeParent int
	active       bool

	span TextSpan

	runes       []rune
	byteOffsets []int
}

type treeInfo struct {
	all      []*blockInfo
	active   []*blockInfo
	ignored  int
	docRunes int
	docBytes int
}

// buildTreeInfo flattens a tree in preorder and records source spans. Ignored
// regions are still traversed for stable document offsets, but are omitted from
// the active set used for matching.
func buildTreeInfo(root *Block, limit int) (*treeInfo, error) {
	info := &treeInfo{}
	if root == nil {
		return info, nil
	}

	seen := make(map[*Block]bool)
	var walk func(node *Block, parent *blockInfo, inheritedIgnore bool, activeParent int) error
	walk = func(node *Block, parent *blockInfo, inheritedIgnore bool, activeParent int) error {
		if node == nil {
			return fmt.Errorf("%w: nil child", ErrInvalidTree)
		}
		if seen[node] {
			return fmt.Errorf("%w: repeated or cyclic block", ErrInvalidTree)
		}
		if len(info.all)+1 > limit {
			return fmt.Errorf("%w: limit is %d", ErrTooManyBlocks, limit)
		}
		seen[node] = true

		runes := []rune(node.Text)
		byteOffsets := make([]int, len(runes)+1)
		for i, r := range runes {
			byteOffsets[i+1] = byteOffsets[i] + utf8.RuneLen(r)
		}

		ignored := inheritedIgnore || node.Ignore
		bi := &blockInfo{
			block:        node,
			parent:       parent,
			activeParent: -1,
			span: TextSpan{
				RuneStart: info.docRunes,
				RuneEnd:   info.docRunes + len(runes),
				ByteStart: info.docBytes,
				ByteEnd:   info.docBytes + len(node.Text),
			},
			runes:       runes,
			byteOffsets: byteOffsets,
		}
		info.all = append(info.all, bi)
		info.docRunes += len(runes)
		info.docBytes += len(node.Text)

		if ignored {
			info.ignored++
		} else {
			bi.active = true
			bi.activeIndex = len(info.active)
			bi.activeParent = activeParent
			info.active = append(info.active, bi)
		}

		childActiveParent := -1
		if bi.active {
			childActiveParent = bi.activeIndex
		}
		for _, child := range node.Children {
			if err := walk(child, bi, ignored, childActiveParent); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(root, nil, false, -1); err != nil {
		return nil, err
	}
	return info, nil
}

func (b *blockInfo) localSpan(start, end int) TextSpan {
	if start < 0 {
		start = 0
	}
	if end > len(b.runes) {
		end = len(b.runes)
	}
	if end < start {
		end = start
	}
	byteStart := b.span.ByteStart + b.byteOffsets[start]
	byteEnd := b.span.ByteStart + b.byteOffsets[end]
	return TextSpan{
		RuneStart: b.span.RuneStart + start,
		RuneEnd:   b.span.RuneStart + end,
		ByteStart: byteStart,
		ByteEnd:   byteEnd,
	}
}

func (b *blockInfo) nodeKey(useIDs bool) string {
	if useIDs {
		return "id:" + b.block.ID
	}
	return "r:" + b.block.Role + "\x00t:" + b.block.Text
}

func (b *blockInfo) parentKey(useIDs bool) string {
	if b.parent == nil {
		return "\x00root"
	}
	return b.parent.nodeKey(useIDs)
}

func (b *blockInfo) parentID() string {
	if b.parent == nil {
		return ""
	}
	return b.parent.block.ID
}

func allHaveIDs(oldInfo, newInfo *treeInfo) bool {
	for _, b := range oldInfo.active {
		if b.block.ID == "" {
			return false
		}
	}
	for _, b := range newInfo.active {
		if b.block.ID == "" {
			return false
		}
	}
	return len(oldInfo.active)+len(newInfo.active) > 0
}
