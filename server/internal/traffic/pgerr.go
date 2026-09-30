package traffic

import (
	"errors"

	"github.com/lib/pq"
)

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). The generator hits this on purpose sometimes
// - e.g. randomly setting a task to 'in_progress' when its assignee
// already has one in progress (the partial unique index in
// scripts/dev_schema.sql) - and it's fine to just skip that one row rather
// than fail the whole tick.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code.Name() == "unique_violation"
}
