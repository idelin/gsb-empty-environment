package main

// Room is an exam room with a seat capacity.
type Room struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

// Exam is one exam and the list of students enrolled in it.
type Exam struct {
	ID       string   `json:"id"`
	Students []string `json:"students"`
}

// Input is the scheduling problem definition.
type Input struct {
	// Slots is the number of time slots available. 0 means "auto":
	// the solver searches increasing slot counts and keeps the best objective.
	Slots int `json:"slots"`
	// SlotsPerDay, if > 0, means the last slot of a day and the first slot of
	// the next day are NOT considered consecutive for penalty purposes.
	SlotsPerDay int    `json:"slotsPerDay"`
	Rooms       []Room `json:"rooms"`
	Exams       []Exam `json:"exams"`
	// RunPenalty[l] overrides the penalty for a maximal run of l consecutive
	// exams (l >= 2). Zero or missing entries fall back to the default
	// triangular scale: l consecutive exams cost l*(l-1)/2.
	RunPenalty []int `json:"runPenalty"`
}

// Placement is one scheduled exam.
type Placement struct {
	Exam       string `json:"exam"`
	Slot       int    `json:"slot"`
	Day        int    `json:"day"`
	SlotInDay  int    `json:"slotInDay"`
	Room       string `json:"room"`
	Enrollment int    `json:"enrollment"`
}

// StudentPenalty reports one student's load.
type StudentPenalty struct {
	Student string `json:"student"`
	Slots   []int  `json:"slots"`
	Penalty int    `json:"penalty"`
}

// Solution is the solver result.
type Solution struct {
	Slots        int              `json:"slots"`
	Placements   []Placement      `json:"placements"`
	MaxPenalty   int              `json:"maxPenalty"`
	TotalPenalty int              `json:"totalPenalty"`
	Proven       bool             `json:"proven"` // false if the node limit aborted the search
	PerStudent   []StudentPenalty `json:"perStudent"`
}

// maxExams is the supported problem size ("最多十二场考试").
const maxExams = 12
