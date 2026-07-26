package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration192AddsDefaultAffinityReservePercentWithoutOverwriting(t *testing.T) {
	content, err := FS.ReadFile("192_add_openai_priority_saturation_affinity_reserve_percent.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'openai_priority_saturation_affinity_reserve_percent'")
	require.Contains(t, sql, "'20'")
	require.Contains(t, sql, "ON CONFLICT (key) DO NOTHING")
	require.NotContains(t, sql, "schema_migrations")
	require.NotContains(t, sql, "UPDATE settings")
}
