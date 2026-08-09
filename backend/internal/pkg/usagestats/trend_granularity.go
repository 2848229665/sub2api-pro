package usagestats

import "strings"

const (
	UsageTrendGranularityHour  = "hour"
	UsageTrendGranularityDay   = "day"
	UsageTrendGranularityWeek  = "week"
	UsageTrendGranularityMonth = "month"
)

// NormalizeUsageTrendGranularity keeps dashboard trend queries on the supported
// calendar buckets. Unknown values preserve the historical day fallback.
func NormalizeUsageTrendGranularity(granularity string) string {
	switch strings.ToLower(strings.TrimSpace(granularity)) {
	case UsageTrendGranularityHour:
		return UsageTrendGranularityHour
	case UsageTrendGranularityWeek:
		return UsageTrendGranularityWeek
	case UsageTrendGranularityMonth:
		return UsageTrendGranularityMonth
	default:
		return UsageTrendGranularityDay
	}
}
