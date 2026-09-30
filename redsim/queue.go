package redsim

// Packet 是离散事件模型中传输的单位，长度单位为字节。
type Packet struct {
	ID      uint64
	Queue   int
	Len     int
	Arrival int64
}

// QueueStats 统计一个队列的收发丢，用于字节守恒复核。
type QueueStats struct {
	Arrived      int
	Dropped      int
	Departed     int
	BytesArrived int64
	BytesDropped int64
	BytesOut     int64
}

// Queue 是绑定了一套 RED 参数的 FIFO 队列。
type Queue struct {
	ID    int
	RED   *RED
	Stats QueueStats
	pkts  []Packet
	head  int
}

func NewQueue(id int, cfg REDConfig) *Queue {
	return &Queue{ID: id, RED: NewRED(cfg)}
}

func (q *Queue) Len() int { return len(q.pkts) - q.head }

func (q *Queue) Head() (Packet, bool) {
	if q.Len() == 0 {
		return Packet{}, false
	}
	return q.pkts[q.head], true
}

func (q *Queue) Push(p Packet) { q.pkts = append(q.pkts, p) }

// Pop 弹出队首；若队列因此排空，记录空闲起点 tick。
func (q *Queue) Pop(now int64) (Packet, bool) {
	if q.Len() == 0 {
		return Packet{}, false
	}
	p := q.pkts[q.head]
	q.head++
	if q.head == len(q.pkts) {
		q.pkts = q.pkts[:0]
		q.head = 0
		q.RED.NoteEmpty(now)
	}
	return p, true
}

// QueuedBytes 返回当前占用字节数（含在途未完成的包）。
func (q *Queue) QueuedBytes() int64 {
	var n int64
	for i := q.head; i < len(q.pkts); i++ {
		n += int64(q.pkts[i].Len)
	}
	return n
}
