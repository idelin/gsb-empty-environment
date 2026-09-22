package main

func safetySample() []Event {
	return []Event{
		{ID: "w-x", Type: EventWrite, Node: NodeA, OpID: "op-write-x", Key: "k", Value: "1", At: 0},
		{ID: "s-x", Type: EventSend, Node: NodeA, To: NodeB, MsgID: "m-x", OpID: "op-write-x", Key: "k", Value: "1", At: 0},
		{ID: "drop-x", Type: EventDrop, Node: NodeA, To: NodeB, MsgID: "m-x", At: 0},
		{ID: "deliver-x", Type: EventDeliver, Node: NodeB, MsgID: "m-x", At: 0},
		{ID: "read-x-at-b", Type: EventRead, Node: NodeB, OpID: "op-read-x", Key: "k", Expected: "1", At: 0},

		{ID: "w-y", Type: EventWrite, Node: NodeA, OpID: "op-write-y", Key: "other", Value: "2", At: 0},
		{ID: "s-y", Type: EventSend, Node: NodeA, To: NodeB, MsgID: "m-y", OpID: "op-write-y", Key: "other", Value: "2", At: 0},
		{ID: "deliver-y", Type: EventDeliver, Node: NodeB, MsgID: "m-y", At: 0},
		{ID: "read-y-at-a", Type: EventRead, Node: NodeA, OpID: "op-read-y", Key: "other", Expected: "2", At: 0},
		{ID: "s-heartbeat", Type: EventSend, Node: NodeA, To: NodeB, MsgID: "m-heartbeat", OpID: "op-write-y", Key: "other", Value: "2", At: 0},
		{ID: "deliver-heartbeat", Type: EventDeliver, Node: NodeB, MsgID: "m-heartbeat", At: 0},
		{ID: "crash-a", Type: EventCrash, Node: NodeA, At: 0},
		{ID: "recover-a", Type: EventRecover, Node: NodeA, At: 0},
	}
}

func livenessSample() []Event {
	return []Event{
		{ID: "crash-b", Type: EventCrash, Node: NodeB, At: 0},
		{ID: "read-b-pending", Type: EventRead, Node: NodeB, OpID: "op-pending-read", Key: "k", Deadline: 10, At: 0},
		{ID: "clock-10", Type: EventClock, At: 10},
	}
}
