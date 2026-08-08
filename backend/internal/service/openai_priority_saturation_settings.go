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
	// SettingKeyOpenAIPrioritySaturationPoolBalanceEnabled enables the
	// account/API-key pool balancing layer on top of priority saturation.
	// It is intentionally independent so the existing saturation order can be
	// rolled out first and the pool split can be enabled per deployment.
	SettingKeyOpenAIPrioritySaturationPoolBalanceEnabled = "openai_priority_saturation_pool_balance_enabled"
	SettingKeyOpenAIPrioritySaturationAPIKeySharePercent = "openai_priority_saturation_api_key_share_percent"
	DefaultOpenAIPrioritySaturationAPIKeySharePercent    = 33
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

func validateOpenAIPrioritySaturationAPIKeySharePercent(percent int) error {
	if percent < 1 || percent > 99 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_API_KEY_SHARE_PERCENT",
			"openai_priority_saturation_api_key_share_percent must be between 1 and 99",
		)
	}
	return nil
}

func normalizeOpenAIPrioritySaturationAPIKeySharePercent(percent int) int {
	if validateOpenAIPrioritySaturationAPIKeySharePercent(percent) != nil {
		return DefaultOpenAIPrioritySaturationAPIKeySharePercent
	}
	return percent
}

func parseOpenAIPrioritySaturationAPIKeySharePercent(raw string) int {
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultOpenAIPrioritySaturationAPIKeySharePercent
	}
	return normalizeOpenAIPrioritySaturationAPIKeySharePercent(percent)
}
