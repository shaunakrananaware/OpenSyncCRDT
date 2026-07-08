package sync

import (
	"fmt"

	"github.com/opensynccrdt/opensynccrdt/pkg/protocol"
)

// Replay streams every operation after afterSeq to a catching-up subscriber, in
// ascending sequence order, split into batches. deliver is called once per
// batch; if it returns an error, replay stops and returns it.
//
// A subscriber that is already up to date receives a single empty, done batch,
// so clients can always wait for done=true.
func (e *Engine) Replay(docID string, afterSeq int64, deliver func(protocol.Replay) error) error {
	ops, err := e.store.GetOpsSince(docID, afterSeq)
	if err != nil {
		return fmt.Errorf("replay: load ops: %w", err)
	}

	total := (len(ops) + e.replayBatch - 1) / e.replayBatch
	if total == 0 {
		total = 1 // always send at least a terminal empty batch
	}

	for batch := 0; batch < total; batch++ {
		start := batch * e.replayBatch
		end := start + e.replayBatch
		if end > len(ops) {
			end = len(ops)
		}

		chunk := ops[start:end]
		replayOps := make([]protocol.ReplayOp, len(chunk))
		for i, op := range chunk {
			replayOps[i] = protocol.ReplayOp{
				Seq:         op.Seq,
				FromSession: op.SessionID,
				Payload:     op.Payload,
			}
		}

		done := batch == total-1
		msg := protocol.NewReplay(docID, replayOps, batch+1, total, done)
		if err := deliver(msg); err != nil {
			return err
		}
	}
	return nil
}
