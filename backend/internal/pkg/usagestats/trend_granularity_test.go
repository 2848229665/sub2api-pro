package usagestats

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeUsageTrendGranularity(t *testing.T) {
	tests := map[string]string{
		"hour":    UsageTrendGranularityHour,
		"DAY":     UsageTrendGranularityDay,
		" week ":  UsageTrendGranularityWeek,
		"Month":   UsageTrendGranularityMonth,
		"":        UsageTrendGranularityDay,
		"quarter": UsageTrendGranularityDay,
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, NormalizeUsageTrendGranularity(input))
		})
	}
}
