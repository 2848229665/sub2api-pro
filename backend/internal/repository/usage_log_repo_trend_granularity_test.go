//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestGetUsageTrendFromAggregatesGroupsDailyRowsByWeekAndMonth(t *testing.T) {
	tests := []struct {
		granularity string
		dateFormat  string
		bucket      string
	}{
		{granularity: "week", dateFormat: "IYYY-IW", bucket: "2026-32"},
		{granularity: "month", dateFormat: "YYYY-MM", bucket: "2026-08"},
	}

	for _, tc := range tests {
		t.Run(tc.granularity, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
			require.NoError(t, err)
			defer db.Close()

			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
			mock.ExpectQuery(`(?s)TO_CHAR\(bucket_date::timestamp, '`+tc.dateFormat+`'\).*SUM\(total_requests\).*FROM usage_dashboard_daily.*GROUP BY date.*ORDER BY MIN\(bucket_date\) ASC`).
				WithArgs(start, end).
				WillReturnRows(sqlmock.NewRows([]string{
					"date",
					"requests",
					"input_tokens",
					"output_tokens",
					"cache_creation_tokens",
					"cache_read_tokens",
					"total_tokens",
					"cost",
					"actual_cost",
				}).AddRow(tc.bucket, 3, 10, 20, 30, 40, 100, 1.25, 0.75))

			repo := newUsageLogRepositoryWithSQL(nil, db)
			trend, err := repo.getUsageTrendFromAggregates(context.Background(), start, end, tc.granularity)

			require.NoError(t, err)
			require.Len(t, trend, 1)
			require.Equal(t, tc.bucket, trend[0].Date)
			require.EqualValues(t, 3, trend[0].Requests)
			require.EqualValues(t, 100, trend[0].TotalTokens)
			require.InDelta(t, 0.75, trend[0].ActualCost, 0.0001)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestShouldUsePreaggregatedTrendSupportsWeekAndMonth(t *testing.T) {
	for _, granularity := range []string{"hour", "day", "week", "month"} {
		require.True(t, shouldUsePreaggregatedTrend(granularity, 0, 0, 0, 0, "", nil, nil, nil, "", nil))
	}

	require.False(t, shouldUsePreaggregatedTrend("month", 42, 0, 0, 0, "", nil, nil, nil, "", nil))
	require.False(t, shouldUsePreaggregatedTrend("quarter", 0, 0, 0, 0, "", nil, nil, nil, "", nil))
}
