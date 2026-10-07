// Package reorder 按稳定段落 id 对段落序列做批量重排：
// 同一批中可以混合删除、移动与内容修改，移动目标以前驱 id 指定。
package reorder

import "fmt"

// Paragraph 是文档中的一个段落，ID 在文档内稳定且唯一。
type Paragraph struct {
	ID      string
	Content string
}

// Move 表示把段落 ID 移动到 Predecessor 之后；
// Predecessor 为空串表示移动到序列最前。
type Move struct {
	ID          string
	Predecessor string
}

// Batch 描述同批应用的删除、移动与内容修改。
type Batch struct {
	Deletes  []string
	Moves    []Move
	Modifies map[string]string
}

// Apply 将一批删除、移动、修改应用到段落序列上，返回新序列。
//
// 语义：
//   - 删除、移动、修改基于同一份旧序列核对（节点序列模型），互不因应用顺序而偏差；
//   - 移动锚点以前驱 id 指定；前驱在同批被删除时，沿旧顺序向前找最近存活段落作为锚点，
//     找不到则移动到最前；
//   - 锚点本身也被移动时，被移动段落跟随锚点落位（锚点链）；
//   - 移动约束成环（含自环）时报错；
//   - 修改/移动已删除或不存在的段落时报错。
func Apply(doc []Paragraph, b Batch) ([]Paragraph, error) {
	pos := make(map[string]int, len(doc))
	for i, p := range doc {
		if _, dup := pos[p.ID]; dup {
			return nil, fmt.Errorf("reorder: duplicate paragraph id %q", p.ID)
		}
		pos[p.ID] = i
	}

	deleted := make(map[string]bool, len(b.Deletes))
	for _, id := range b.Deletes {
		if _, ok := pos[id]; !ok {
			return nil, fmt.Errorf("reorder: delete of unknown paragraph %q", id)
		}
		deleted[id] = true
	}

	for id := range b.Modifies {
		if _, ok := pos[id]; !ok {
			return nil, fmt.Errorf("reorder: modify of unknown paragraph %q", id)
		}
		if deleted[id] {
			return nil, fmt.Errorf("reorder: modify of deleted paragraph %q", id)
		}
	}

	moved := make(map[string]bool, len(b.Moves))
	for _, m := range b.Moves {
		if _, ok := pos[m.ID]; !ok {
			return nil, fmt.Errorf("reorder: move of unknown paragraph %q", m.ID)
		}
		if deleted[m.ID] {
			return nil, fmt.Errorf("reorder: move of deleted paragraph %q", m.ID)
		}
		if moved[m.ID] {
			return nil, fmt.Errorf("reorder: duplicate move of paragraph %q", m.ID)
		}
		if m.Predecessor != "" {
			if _, ok := pos[m.Predecessor]; !ok {
				return nil, fmt.Errorf("reorder: move of %q targets unknown predecessor %q", m.ID, m.Predecessor)
			}
			if m.Predecessor == m.ID {
				return nil, fmt.Errorf("reorder: paragraph %q cannot be moved after itself", m.ID)
			}
		}
		moved[m.ID] = true
	}

	// 解析每个移动的最终锚点：沿旧顺序从指定前驱向前找最近存活段落。
	anchor := make(map[string]string, len(b.Moves))
	for _, m := range b.Moves {
		anchor[m.ID] = resolveAnchor(doc, pos, deleted, m)
	}

	// 循环移动约束检测：沿锚点链（仅经过被移动节点）前进，回访即环。
	for _, m := range b.Moves {
		seen := map[string]bool{m.ID: true}
		for cur := anchor[m.ID]; cur != "" && moved[cur]; cur = anchor[cur] {
			if seen[cur] {
				return nil, fmt.Errorf("reorder: circular move constraint involving %q", cur)
			}
			seen[cur] = true
		}
	}

	// 按旧相对顺序把被移动节点挂到锚点下。
	children := make(map[string][]string)
	front := []string{}
	for _, p := range doc {
		if !moved[p.ID] {
			continue
		}
		a := anchor[p.ID]
		if a == "" {
			front = append(front, p.ID)
		} else {
			children[a] = append(children[a], p.ID)
		}
	}

	content := make(map[string]string, len(doc))
	for _, p := range doc {
		content[p.ID] = p.Content
	}
	for id, c := range b.Modifies {
		content[id] = c
	}

	order := make([]string, 0, len(doc)-len(b.Deletes))
	emitted := make(map[string]bool, len(b.Moves))
	var emitChain func(anchorID string)
	emitChain = func(anchorID string) {
		for _, id := range children[anchorID] {
			order = append(order, id)
			emitted[id] = true
			emitChain(id)
		}
	}
	for _, id := range front {
		order = append(order, id)
		emitted[id] = true
		emitChain(id)
	}
	for _, p := range doc {
		if deleted[p.ID] || moved[p.ID] {
			continue
		}
		order = append(order, p.ID)
		emitChain(p.ID)
	}
	if len(emitted) != len(b.Moves) {
		return nil, fmt.Errorf("reorder: unresolved move constraints")
	}

	out := make([]Paragraph, 0, len(order))
	for _, id := range order {
		out = append(out, Paragraph{ID: id, Content: content[id]})
	}
	return out, nil
}

// resolveAnchor 计算移动 m 的最终锚点：从指定前驱开始沿旧顺序向前，
// 跳过已删除段落与自身，返回最近存活段落 id；没有则返回 ""（最前）。
func resolveAnchor(doc []Paragraph, pos map[string]int, deleted map[string]bool, m Move) string {
	if m.Predecessor == "" {
		return ""
	}
	for i := pos[m.Predecessor]; i >= 0; i-- {
		id := doc[i].ID
		if id == m.ID || deleted[id] {
			continue
		}
		return id
	}
	return ""
}
