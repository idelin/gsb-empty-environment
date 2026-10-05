package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// MaxExams 是本工具支持的考试数量上限。
const MaxExams = 12

// Exam 表示一场考试。
type Exam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Room 表示一个考场。
type Room struct {
	ID       string `json:"id"`
	Capacity int    `json:"capacity"`
}

// Input 是排考问题的完整输入。
type Input struct {
	NumSlots int `json:"num_slots"`
	// GapPenalties[k] 是同一学生两场考试相隔 k+1 个时段的分级惩罚，
	// 超出列表长度的间隔惩罚为 0。例如 [10,4,1] 表示相邻时段罚 10，
	// 隔一个时段罚 4，隔两个时段罚 1，更远不罚。
	GapPenalties []int               `json:"gap_penalties"`
	Exams        []Exam              `json:"exams"`
	Rooms        []Room              `json:"rooms"`
	Enrollments  map[string][]string `json:"enrollments"` // 学生 -> 考试 ID 列表
}

// Assignment 是求解结果：每场考试的时段与考场。
type Assignment struct {
	Slot []int    `json:"slot"` // Slot[i] 是考试 i 的时段（0 起）
	Room []string `json:"room"` // Room[i] 是考试 i 的考场 ID
}

// LoadInput 从 JSON 文件读取输入；path 为空时使用内置示例。
func LoadInput(path string) (*Input, error) {
	var in Input
	if path == "" {
		in = defaultInput()
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, fmt.Errorf("解析输入 JSON 失败: %w", err)
		}
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	return &in, nil
}

// Validate 检查输入的合法性。
func (in *Input) Validate() error {
	if len(in.Exams) == 0 {
		return fmt.Errorf("至少需要一场考试")
	}
	if len(in.Exams) > MaxExams {
		return fmt.Errorf("考试数量 %d 超过上限 %d", len(in.Exams), MaxExams)
	}
	if in.NumSlots <= 0 {
		return fmt.Errorf("时段数必须为正")
	}
	if len(in.GapPenalties) == 0 {
		return fmt.Errorf("gap_penalties 不能为空")
	}
	for i, p := range in.GapPenalties {
		if p < 0 {
			return fmt.Errorf("gap_penalties[%d] 不能为负", i)
		}
	}
	seen := map[string]bool{}
	for _, e := range in.Exams {
		if e.ID == "" {
			return fmt.Errorf("考试 ID 不能为空")
		}
		if seen[e.ID] {
			return fmt.Errorf("考试 ID 重复: %s", e.ID)
		}
		seen[e.ID] = true
	}
	for _, r := range in.Rooms {
		if r.ID == "" || r.Capacity <= 0 {
			return fmt.Errorf("考场 %q 非法：ID 不能为空且容量必须为正", r.ID)
		}
	}
	for stu, exams := range in.Enrollments {
		dup := map[string]bool{}
		for _, id := range exams {
			if !seen[id] {
				return fmt.Errorf("学生 %s 选修了不存在的考试 %s", stu, id)
			}
			if dup[id] {
				return fmt.Errorf("学生 %s 重复选修考试 %s", stu, id)
			}
			dup[id] = true
		}
	}
	return nil
}

// Penalty 返回间隔 gap 个时段的两场考试对同一学生的分级惩罚。
func (in *Input) Penalty(gap int) int {
	if gap <= 0 || gap > len(in.GapPenalties) {
		return 0
	}
	return in.GapPenalties[gap-1]
}

// examIndex 返回考试 ID 到下标的映射。
func (in *Input) examIndex() map[string]int {
	idx := make(map[string]int, len(in.Exams))
	for i, e := range in.Exams {
		idx[e.ID] = i
	}
	return idx
}

// StudentExams 返回每个学生选修的考试下标列表（按学生名排序，确定性输出）。
func (in *Input) StudentExams() ([]string, [][]int) {
	idx := in.examIndex()
	names := make([]string, 0, len(in.Enrollments))
	for name := range in.Enrollments {
		names = append(names, name)
	}
	sort.Strings(names)
	lists := make([][]int, len(names))
	for i, name := range names {
		for _, id := range in.Enrollments[name] {
			lists[i] = append(lists[i], idx[id])
		}
		sort.Ints(lists[i])
	}
	return names, lists
}

// enrollmentSize 返回每场考试的选课人数。
func (in *Input) enrollmentSize() []int {
	size := make([]int, len(in.Exams))
	idx := in.examIndex()
	for _, exams := range in.Enrollments {
		for _, id := range exams {
			size[idx[id]]++
		}
	}
	return size
}

// defaultInput 是内置示例：8 门考试、5 个时段、3 个考场、12 名学生。
func defaultInput() Input {
	return Input{
		NumSlots:     5,
		GapPenalties: []int{10, 4, 1},
		Exams: []Exam{
			{ID: "MA", Name: "高等数学"},
			{ID: "PH", Name: "大学物理"},
			{ID: "CS", Name: "数据结构"},
			{ID: "EN", Name: "英语"},
			{ID: "CH", Name: "化学"},
			{ID: "EC", Name: "经济学"},
			{ID: "BI", Name: "生物"},
			{ID: "HI", Name: "历史"},
		},
		Rooms: []Room{
			{ID: "A101", Capacity: 8},
			{ID: "A102", Capacity: 6},
			{ID: "B201", Capacity: 4},
		},
		Enrollments: map[string][]string{
			"s01": {"MA", "PH", "CS"},
			"s02": {"MA", "EN"},
			"s03": {"MA", "CH", "BI"},
			"s04": {"PH", "CS", "EN"},
			"s05": {"PH", "EC"},
			"s06": {"CS", "CH"},
			"s07": {"EN", "HI"},
			"s08": {"CH", "BI", "HI"},
			"s09": {"EC", "HI"},
			"s10": {"MA", "EC"},
			"s11": {"CS", "BI"},
			"s12": {"PH", "HI"},
		},
	}
}
