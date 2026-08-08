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
	SettingKeyOpenAIPrioritySaturationPoolBalanceEnabled   = "openai_priority_saturation_pool_balance_enabled"
	DefaultOpenAIPrioritySaturationPoolBalanceEnabled      = true
	SettingKeyOpenAIPrioritySaturationAccountSharePercent  = "openai_priority_saturation_account_share_percent"
	DefaultOpenAIPrioritySaturationAccountSharePercent     = 67
	SettingKeyOpenAIPrioritySaturationAPIKeySharePercent   = "openai_priority_saturation_api_key_share_percent"
	DefaultOpenAIPrioritySaturationAPIKeySharePercent      = 33
	SettingKeyOpenAIPrioritySaturationEnterHighLoadPercent = "openai_priority_saturation_enter_high_load_percent"
	DefaultOpenAIPrioritySaturationEnterHighLoadPercent    = 70
	SettingKeyOpenAIPrioritySaturationExitHighLoadPercent  = "openai_priority_saturation_exit_high_load_percent"
	DefaultOpenAIPrioritySaturationExitHighLoadPercent     = 50
)

func parseOpenAIPrioritySaturationEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

func parseOpenAIPrioritySaturationPoolBalanceEnabled(raw string) bool {
	return strings.EqualFold(strings.TrimSpace(raw), "true")
}

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

func validateOpenAIPrioritySaturationPoolShares(accountPercent, apiKeyPercent int) error {
	if accountPercent < 1 || accountPercent > 99 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_ACCOUNT_SHARE_PERCENT",
			"openai_priority_saturation_account_share_percent must be between 1 and 99",
		)
	}
	if err := validateOpenAIPrioritySaturationAPIKeySharePercent(apiKeyPercent); err != nil {
		return err
	}
	if accountPercent+apiKeyPercent != 100 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_POOL_SHARES",
			"openai_priority_saturation_account_share_percent and openai_priority_saturation_api_key_share_percent must add up to 100",
		)
	}
	return nil
}

func normalizeOpenAIPrioritySaturationPoolShares(accountPercent, apiKeyPercent int) (int, int) {
	accountPercent = normalizeOpenAIPrioritySaturationAccountSharePercent(accountPercent)
	apiKeyPercent = normalizeOpenAIPrioritySaturationAPIKeySharePercent(apiKeyPercent)
	if accountPercent+apiKeyPercent != 100 {
		return DefaultOpenAIPrioritySaturationAccountSharePercent, DefaultOpenAIPrioritySaturationAPIKeySharePercent
	}
	return accountPercent, apiKeyPercent
}

func normalizeOpenAIPrioritySaturationAccountSharePercent(percent int) int {
	if percent < 1 || percent > 99 {
		return DefaultOpenAIPrioritySaturationAccountSharePercent
	}
	return percent
}

func parseOpenAIPrioritySaturationAccountSharePercent(raw string) int {
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultOpenAIPrioritySaturationAccountSharePercent
	}
	return normalizeOpenAIPrioritySaturationAccountSharePercent(percent)
}

func parseOpenAIPrioritySaturationPoolShares(accountRaw, apiKeyRaw string) (int, int) {
	if strings.TrimSpace(apiKeyRaw) == "" && strings.TrimSpace(accountRaw) != "" {
		accountPercent := parseOpenAIPrioritySaturationAccountSharePercent(accountRaw)
		return accountPercent, 100 - accountPercent
	}
	apiKeyPercent := parseOpenAIPrioritySaturationAPIKeySharePercent(apiKeyRaw)
	if strings.TrimSpace(accountRaw) == "" {
		return 100 - apiKeyPercent, apiKeyPercent
	}
	accountPercent := parseOpenAIPrioritySaturationAccountSharePercent(accountRaw)
	return normalizeOpenAIPrioritySaturationPoolShares(accountPercent, apiKeyPercent)
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

func validateOpenAIPrioritySaturationLoadThresholds(enterPercent, exitPercent int) error {
	if enterPercent < 1 || enterPercent > 100 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_ENTER_HIGH_LOAD_PERCENT",
			"openai_priority_saturation_enter_high_load_percent must be between 1 and 100",
		)
	}
	if exitPercent < 0 || exitPercent > 99 {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_EXIT_HIGH_LOAD_PERCENT",
			"openai_priority_saturation_exit_high_load_percent must be between 0 and 99",
		)
	}
	if exitPercent >= enterPercent {
		return infraerrors.BadRequest(
			"INVALID_OPENAI_PRIORITY_SATURATION_LOAD_HYSTERESIS",
			"openai_priority_saturation_exit_high_load_percent must be lower than openai_priority_saturation_enter_high_load_percent",
		)
	}
	return nil
}

func normalizeOpenAIPrioritySaturationEnterHighLoadPercent(percent int) int {
	if percent < 1 || percent > 100 {
		return DefaultOpenAIPrioritySaturationEnterHighLoadPercent
	}
	return percent
}

func normalizeOpenAIPrioritySaturationExitHighLoadPercent(percent int) int {
	if percent < 0 || percent > 99 {
		return DefaultOpenAIPrioritySaturationExitHighLoadPercent
	}
	return percent
}

func parseOpenAIPrioritySaturationEnterHighLoadPercent(raw string) int {
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultOpenAIPrioritySaturationEnterHighLoadPercent
	}
	return normalizeOpenAIPrioritySaturationEnterHighLoadPercent(percent)
}

func parseOpenAIPrioritySaturationExitHighLoadPercent(raw string) int {
	percent, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultOpenAIPrioritySaturationExitHighLoadPercent
	}
	return normalizeOpenAIPrioritySaturationExitHighLoadPercent(percent)
}
