package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration196DefaultsAdaptivePoolSchedulingWithoutOverwriting(t *testing.T) {
	content, err := FS.ReadFile("196_enable_openai_adaptive_pool_scheduling.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	for _, setting := range []string{
		"openai_priority_saturation_pool_balance_enabled",
		"openai_priority_saturation_account_share_percent",
		"openai_priority_saturation_api_key_share_percent",
		"openai_priority_saturation_enter_high_load_percent",
		"openai_priority_saturation_exit_high_load_percent",
	} {
		require.Contains(t, sql, "'"+setting+"'")
	}
	require.Contains(t, sql, "'true'")
	require.Contains(t, sql, "'33'")
	require.Contains(t, sql, "'70'")
	require.Contains(t, sql, "'50'")
	require.Equal(t, 5, strings.Count(sql, "ON CONFLICT (key) DO NOTHING"))
	require.NotContains(t, sql, "UPDATE settings")
}
