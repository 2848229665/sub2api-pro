package service

import (
	"log/slog"
	"time"
)

const (
	openAIAccountSchedulerPolicyLegacy             = "legacy"
	openAIAccountSchedulerPolicyWeightedTopK       = "weighted_topk"
	openAIAccountSchedulerPolicyPrioritySaturation = "priority_saturation"
	openAIAccountSchedulerMetricsLogInterval       = 1024
)

func (s *OpenAIGatewayService) observeOpenAIAccountSchedule(
	policy string,
	groupID *int64,
	platform string,
	requestedModel string,
	excludedAccountCount int,
	elapsed time.Duration,
	selection *AccountSelectionResult,
	decision OpenAIAccountScheduleDecision,
	scheduleErr error,
) {
	if s == nil {
		return
	}
	latencyMs := decision.LatencyMs
	if latencyMs <= 0 {
		latencyMs = elapsed.Milliseconds()
	}
	selectedAccountID := decision.SelectedAccountID
	if selectedAccountID <= 0 && selection != nil && selection.Account != nil {
		selectedAccountID = selection.Account.ID
	}

	slog.Debug(
		"openai_account_schedule_decision",
		"policy", policy,
		"group_id", derefGroupID(groupID),
		"platform", normalizeOpenAICompatiblePlatform(platform),
		"requested_model", requestedModel,
		"layer", decision.Layer,
		"selected_account_id", selectedAccountID,
		"selected_account_type", decision.SelectedAccountType,
		"candidate_count", decision.CandidateCount,
		"excluded_account_count", excludedAccountCount,
		"top_k", decision.TopK,
		"latency_ms", latencyMs,
		"general_limit", decision.GeneralLimit,
		"hard_limit", decision.HardLimit,
		"affinity_reserve", decision.AffinityReserve,
		"general_reject_count", decision.GeneralRejectCount,
		"affinity_reserve_hit", decision.AffinityReserveHit,
		"temporary_overflow", decision.TemporaryOverflow,
		"affinity_wait", decision.AffinityWait,
		"affinity_rejected", decision.AffinityRejected,
		"preferred_pool", decision.PreferredPool,
		"selected_pool", decision.SelectedPool,
		"pool_mode", decision.PoolMode,
		"account_active", decision.AccountActive,
		"key_active", decision.KeyActive,
		"account_waiting", decision.AccountWaiting,
		"key_waiting", decision.KeyWaiting,
		"account_capacity", decision.AccountCapacity,
		"key_capacity", decision.KeyCapacity,
		"key_budget", decision.KeyBudget,
		"fallback_reason", decision.FallbackReason,
		"budget_reason", decision.BudgetReason,
		"error", scheduleErr,
	)

	if selection != nil && selection.WaitPlan != nil {
		waitReason := selection.WaitPlan.Reason
		if waitReason == "" {
			if decision.AffinityWait {
				waitReason = "affinity_owner_busy"
			} else {
				waitReason = "scheduler_fallback"
			}
		}
		selectedPriority := 0
		if selection.Account != nil {
			selectedPriority = selection.Account.Priority
		}
		waitCandidateCount := selection.WaitPlan.CandidateCount
		if waitCandidateCount <= 0 {
			waitCandidateCount = len(selection.WaitPlan.Candidates)
		}
		slog.Info(
			"openai_account_schedule_wait_plan",
			"policy", policy,
			"group_id", derefGroupID(groupID),
			"platform", normalizeOpenAICompatiblePlatform(platform),
			"requested_model", requestedModel,
			"layer", decision.Layer,
			"reason", waitReason,
			"selected_account_id", selectedAccountID,
			"selected_account_type", decision.SelectedAccountType,
			"selected_priority", selectedPriority,
			"candidate_count", decision.CandidateCount,
			"excluded_account_count", excludedAccountCount,
			"general_reject_count", decision.GeneralRejectCount,
			"general_limit", decision.GeneralLimit,
			"hard_limit", decision.HardLimit,
			"affinity_reserve", decision.AffinityReserve,
			"affinity_wait", decision.AffinityWait,
			"wait_timeout_ms", selection.WaitPlan.Timeout.Milliseconds(),
			"max_waiting", selection.WaitPlan.MaxWaiting,
			"wait_candidate_count", waitCandidateCount,
			"logged_wait_candidate_count", len(selection.WaitPlan.Candidates),
			"wait_candidates_truncated", selection.WaitPlan.CandidatesTruncated,
			"wait_candidates", selection.WaitPlan.Candidates,
		)
	}

	if s.openaiSchedulerMetricsLogCounter.Add(1)%openAIAccountSchedulerMetricsLogInterval != 0 {
		return
	}
	metrics := s.SnapshotOpenAIAccountSchedulerMetrics()
	slog.Info(
		"openai_account_scheduler_metrics",
		"policy", policy,
		"select_total", metrics.SelectTotal,
		"sticky_previous_hit_total", metrics.StickyPreviousHitTotal,
		"sticky_session_hit_total", metrics.StickySessionHitTotal,
		"load_balance_select_total", metrics.LoadBalanceSelectTotal,
		"account_switch_total", metrics.AccountSwitchTotal,
		"scheduler_latency_ms_total", metrics.SchedulerLatencyMsTotal,
		"scheduler_latency_ms_avg", metrics.SchedulerLatencyMsAvg,
		"sticky_hit_ratio", metrics.StickyHitRatio,
		"account_switch_rate", metrics.AccountSwitchRate,
		"load_skew_avg", metrics.LoadSkewAvg,
		"runtime_stats_account_count", metrics.RuntimeStatsAccountCount,
		"general_reject_total", metrics.GeneralRejectTotal,
		"affinity_reserve_hit_total", metrics.AffinityReserveHitTotal,
		"temporary_overflow_total", metrics.TemporaryOverflowTotal,
		"affinity_wait_total", metrics.AffinityWaitTotal,
		"affinity_rejected_total", metrics.AffinityRejectedTotal,
	)
}
