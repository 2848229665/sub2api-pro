package service

import (
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// SettingKeyOpenAIPrioritySaturationEnabled independently enables the
// deterministic priority-saturation scheduler. It must not be conflated with
// the existing weighted advanced scheduler switch.
const SettingKeyOpenAIPrioritySaturationEnabled = "openai_priority_saturation_enabled"

const (
	SettingKeyOpenAIPrioritySaturationAffinityReservePercent = "openai_priority_saturation_affinity_reserve_percent"
	DefaultOpenAIPrioritySaturationAffinityReservePercent    = 20
)

func validateOpenAIPrioritySaturationAffinityReservePercent(percent int) error {
	if percent < 0 || percent > 99 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_AFFINITY_RESERVE_PERCENT",
			"openai_priority_saturation_affinity_reserve_percent must be between 0 and 99",
		)
	}
	return nil
}

func normalizeOpenAIPrioritySaturationAffinityReservePercent(percent int) int {
	if validateOpenAIPrioritySaturationAffinityReservePercent(percent) != nil {
		return DefaultOpenAIPrioritySaturationAffinityReservePercent
	}
	return percent
}

func parseOpenAIPrioritySaturationAffinityReservePercent(raw string) int {
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultOpenAIPrioritySaturationAffinityReservePercent
	}
	return normalizeOpenAIPrioritySaturationAffinityReservePercent(percent)
}
