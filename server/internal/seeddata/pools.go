// Package seeddata generates rows for the demo schema - used both by the
// one-time cmd/seed tool and by internal/normalizer to top tables back up
// toward baseline.
package seeddata

import (
	"fmt"
	"strings"
)

// RandomPools builds, for {alias: parentTable} pairs, a WITH clause
// materializing each parent's ids into an array once, a CROSS JOIN
// clause, and the indexing expression to pick one at random per output
// row. random() is called directly in that expression, not inside a bare
// or LATERAL subquery - required to force per-row evaluation, not a
// single InitPlan for the whole statement (see scripts/seed_data.sql in
// the main Worm repo for the failure mode this avoids).
func RandomPools(parents map[string]string) (withClause, joinClause string, pick map[string]string) {
	var ctes, joins []string
	pick = make(map[string]string)
	for alias, table := range parents {
		ctes = append(ctes, fmt.Sprintf("%s_pool AS (SELECT array_agg(id) AS ids FROM %s)", alias, table))
		joins = append(joins, alias+"_pool")
		pick[alias] = fmt.Sprintf("%s_pool.ids[1 + floor(random() * array_length(%s_pool.ids, 1))::int]", alias, alias)
	}
	return strings.Join(ctes, ", "), strings.Join(joins, " CROSS JOIN "), pick
}
