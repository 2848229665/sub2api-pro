package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration191DefaultsPrioritySaturationEnabledIndependently(t *testing.T) {
	content, err := FS.ReadFile("191_enable_priority_saturation_scheduler.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "'openai_priority_saturation_enabled'")
	require.Contains(t, sql, "'true'")
	require.NotContains(t, sql, "'openai_advanced_scheduler_enabled'")
	require.NotContains(t, sql, "THEN 'false'")
	require.Contains(t, sql, "ON CONFLICT (key) DO NOTHING")
}
