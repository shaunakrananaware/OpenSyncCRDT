package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// scanOpRows materializes a StoredOp slice from a database/sql result set whose
// columns are (doc_id, seq, session_id, payload, created_at) with created_at
// stored as unix milliseconds. Shared by the SQLite and MySQL backends, which
// both use the standard database/sql driver interface.
func scanOpRows(rows *sql.Rows) ([]StoredOp, error) {
	ops := []StoredOp{}
	for rows.Next() {
		var (
			op        StoredOp
			createdMs int64
		)
		if err := rows.Scan(&op.DocID, &op.Seq, &op.SessionID, &op.Payload, &createdMs); err != nil {
			return nil, fmt.Errorf("scan op: %w", err)
		}
		op.CreatedAt = time.UnixMilli(createdMs).UTC()
		ops = append(ops, op)
	}
	return ops, rows.Err()
}
