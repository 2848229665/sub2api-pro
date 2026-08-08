package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const openAIAdaptivePoolStateTTLSeconds = 60 * 60

var acquireOpenAIAdaptivePoolSlotScript = redis.NewScript(`
	redis.replicate_commands()

	local stateKey = KEYS[1]
	local slotTTL = tonumber(ARGV[1])
	local liveTTL = tonumber(ARGV[2])
	local requestID = ARGV[3]
	local accountShare = tonumber(ARGV[4])
	local keyShare = tonumber(ARGV[5])
	local enterHigh = tonumber(ARGV[6])
	local exitHigh = tonumber(ARGV[7])
	local exitStableSeconds = tonumber(ARGV[8])
	local stateTTL = tonumber(ARGV[9])
	local candidateCount = tonumber(ARGV[10])
	local metadataOffset = 11

	local now = tonumber(redis.call('TIME')[1])
	local candidates = {}
	local accountActive = 0
	local keyActive = 0
	local accountCapacity = 0
	local keyCapacity = 0
	local accountWaiting = 0
	local keyWaiting = 0
	local accountUnlimited = false
	local keyUnlimited = false
	local accountCount = 0
	local keyCount = 0

	for i = 1, candidateCount do
		local keyOffset = 2 + ((i - 1) * 3)
		local argOffset = metadataOffset + ((i - 1) * 4)
		local regularKey = KEYS[keyOffset]
		local liveKey = KEYS[keyOffset + 1]
		local waitKey = KEYS[keyOffset + 2]
		local accountID = ARGV[argOffset]
		local pool = tonumber(ARGV[argOffset + 1])
		local priority = tonumber(ARGV[argOffset + 2])
		local limit = tonumber(ARGV[argOffset + 3])

		redis.call('ZREMRANGEBYSCORE', regularKey, '-inf', now - slotTTL)
		redis.call('ZREMRANGEBYSCORE', liveKey, '-inf', now - liveTTL)
		local active = redis.call('ZCARD', regularKey) + redis.call('ZCARD', liveKey)
		local waiting = tonumber(redis.call('GET', waitKey) or '0')
		candidates[i] = {
			id = accountID,
			pool = pool,
			priority = priority,
			limit = limit,
			active = active,
			regularKey = regularKey,
		}

		if pool == 0 then
			accountCount = accountCount + 1
			accountActive = accountActive + active
			accountWaiting = accountWaiting + waiting
			if limit <= 0 then
				accountUnlimited = true
			else
				accountCapacity = accountCapacity + limit
			end
		else
			keyCount = keyCount + 1
			keyActive = keyActive + active
			keyWaiting = keyWaiting + waiting
			if limit <= 0 then
				keyUnlimited = true
			else
				keyCapacity = keyCapacity + limit
			end
		end
	end

	local accountObservedCapacity = accountCapacity
	local keyObservedCapacity = keyCapacity
	if accountUnlimited then accountObservedCapacity = 0 end
	if keyUnlimited then keyObservedCapacity = 0 end

	local accountUsage = 0
	if not accountUnlimited and accountCapacity > 0 then
		accountUsage = math.floor((accountActive * 100) / accountCapacity)
	end

	local mode = redis.call('HGET', stateKey, 'mode') or 'low'
	if mode ~= 'high' then mode = 'low' end
	local enterPressure = accountWaiting > 0
		or keyWaiting > 0
		or (not accountUnlimited and accountCapacity > 0 and accountActive >= accountCapacity)
		or (not accountUnlimited and accountCapacity > 0 and accountUsage >= enterHigh)

	if enterPressure then
		mode = 'high'
		redis.call('HSET', stateKey, 'mode', mode, 'low_since', 0)
	elseif mode == 'high' then
		local canExit = accountWaiting == 0
			and keyWaiting == 0
			and (accountUnlimited or accountCapacity == 0 or accountUsage <= exitHigh)
		if canExit then
			local lowSince = tonumber(redis.call('HGET', stateKey, 'low_since') or '0')
			if lowSince <= 0 then
				lowSince = now
				redis.call('HSET', stateKey, 'low_since', lowSince)
			end
			if now - lowSince >= exitStableSeconds then
				mode = 'low'
				redis.call('HSET', stateKey, 'mode', mode, 'low_since', 0)
			end
		else
			redis.call('HSET', stateKey, 'low_since', 0)
		end
	else
		redis.call('HSET', stateKey, 'mode', mode, 'low_since', 0)
	end

	local totalActive = accountActive + keyActive
	local preferredPool = 0
	local budgetReason = 'low_concurrency_ratio'
	local keyBudget = math.floor((((totalActive + 1) * keyShare) + 99) / 100)

	if accountCount == 0 then
		preferredPool = 1
	elseif keyCount == 0 then
		preferredPool = 0
	elseif mode == 'high' then
		budgetReason = 'high_concurrency_headroom'
		if not keyUnlimited then keyBudget = keyCapacity else keyBudget = 0 end

		local accountProjectedNumerator = accountActive + 1
		local keyProjectedNumerator = keyActive + 1
		local accountProjectedDenominator = accountCapacity
		local keyProjectedDenominator = keyCapacity
		if accountUnlimited then accountProjectedDenominator = math.max(accountActive + (accountCount * 1000), 1) end
		if keyUnlimited then keyProjectedDenominator = math.max(keyActive + (keyCount * 1000), 1) end
		if accountProjectedDenominator <= 0 then accountProjectedDenominator = 1 end
		if keyProjectedDenominator <= 0 then keyProjectedDenominator = 1 end

		local accountScore = accountProjectedNumerator * keyProjectedDenominator
		local keyScore = keyProjectedNumerator * accountProjectedDenominator
		if keyScore < accountScore then
			preferredPool = 1
		elseif keyScore == accountScore then
			local highCursor = tonumber(redis.call('HGET', stateKey, 'high_pool_cursor') or '0')
			preferredPool = highCursor % 2
			redis.call('HINCRBY', stateKey, 'high_pool_cursor', 1)
		end
	else
		local lowSequence = tonumber(redis.call('HGET', stateKey, 'low_sequence') or '0')
		local sequencePrefersKey = false
		if accountShare == 67 and keyShare == 33 then
			sequencePrefersKey = (lowSequence % 3) == 2
		else
			local position = lowSequence % 100
			local before = math.floor(((position * keyShare) + 1) / 100)
			local after = math.floor(((((position + 1) * keyShare) + 1) / 100))
			sequencePrefersKey = after > before
		end
		redis.call('HINCRBY', stateKey, 'low_sequence', 1)

		if totalActive > 0 and (keyActive * 100) >= (totalActive * keyShare) then
			preferredPool = 0
		elseif totalActive >= 2 and ((keyActive + 1) * 100) <= ((totalActive + 1) * math.min(keyShare + 1, 100)) then
			preferredPool = 1
		elseif sequencePrefersKey then
			preferredPool = 1
		end
	end

	local function hasRoom(pool)
		for i = 1, candidateCount do
			local candidate = candidates[i]
			if candidate.pool == pool and (candidate.limit <= 0 or candidate.active < candidate.limit) then
				return true
			end
		end
		return false
	end

	local selectedPool = preferredPool
	local fallbackReason = ''
	if not hasRoom(selectedPool) then
		local alternatePool = 1 - selectedPool
		if hasRoom(alternatePool) then
			selectedPool = alternatePool
			fallbackReason = 'preferred_pool_unavailable'
		else
			fallbackReason = 'both_pools_busy'
		end
	end

	local poolCandidates = {}
	for i = 1, candidateCount do
		if candidates[i].pool == selectedPool then
			table.insert(poolCandidates, candidates[i])
		end
	end
	if #poolCandidates == 0 then
		selectedPool = 1 - selectedPool
		for i = 1, candidateCount do
			if candidates[i].pool == selectedPool then
				table.insert(poolCandidates, candidates[i])
			end
		end
		fallbackReason = 'preferred_pool_empty'
	end

	local function isBetter(candidate, selected)
		if selected == nil then return true end
		if candidate.priority < selected.priority then return true end
		if candidate.priority > selected.priority then return false end
		return tonumber(candidate.id) < tonumber(selected.id)
	end

	local selected = nil
	for index = 1, #poolCandidates do
		local candidate = poolCandidates[index]
		if (candidate.limit <= 0 or candidate.active < candidate.limit) and isBetter(candidate, selected) then
			selected = candidate
		end
	end

	local acquired = 0
	if selected ~= nil then
		redis.call('ZADD', selected.regularKey, now, requestID)
		redis.call('EXPIRE', selected.regularKey, slotTTL)
		acquired = 1
	else
		for index = 1, #poolCandidates do
			local candidate = poolCandidates[index]
			if isBetter(candidate, selected) then selected = candidate end
		end
	end

	redis.call('EXPIRE', stateKey, stateTTL)

	local selectedID = ''
	if selected ~= nil then selectedID = selected.id end
	local modeCode = 0
	if mode == 'high' then modeCode = 1 end
	return {
		acquired,
		selectedID,
		preferredPool,
		selectedPool,
		modeCode,
		accountActive,
		keyActive,
		accountObservedCapacity,
		keyObservedCapacity,
		accountWaiting,
		keyWaiting,
		keyBudget,
		fallbackReason,
		budgetReason,
		now
	}
`)

func openAIAdaptivePoolStateKey(scope string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(scope)))
	return "openai:adaptive_pool:" + hex.EncodeToString(digest[:16])
}

func adaptivePoolCode(pool string) (int, error) {
	switch pool {
	case service.OpenAIAdaptivePoolAccount:
		return 0, nil
	case service.OpenAIAdaptivePoolAPIKey:
		return 1, nil
	default:
		return 0, fmt.Errorf("unsupported OpenAI adaptive pool %q", pool)
	}
}

func adaptivePoolName(code int64) string {
	if code == 1 {
		return service.OpenAIAdaptivePoolAPIKey
	}
	return service.OpenAIAdaptivePoolAccount
}

func redisScriptStringAt(raw any, index int) (string, error) {
	values, ok := raw.([]any)
	if !ok || index < 0 || index >= len(values) {
		return "", fmt.Errorf("unexpected redis script result at index %d", index)
	}
	switch value := values[index].(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case nil:
		return "", nil
	default:
		return fmt.Sprint(value), nil
	}
}

func (c *concurrencyCache) AcquireOpenAIAdaptivePoolSlot(
	ctx context.Context,
	req service.OpenAIAdaptivePoolAcquireRequest,
	requestID string,
) (*service.OpenAIAdaptivePoolAcquireDecision, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("OpenAI adaptive pool cache is unavailable")
	}
	if strings.TrimSpace(req.Scope) == "" || strings.TrimSpace(requestID) == "" {
		return nil, fmt.Errorf("OpenAI adaptive pool scope and request id are required")
	}
	if len(req.Candidates) == 0 {
		return &service.OpenAIAdaptivePoolAcquireDecision{}, nil
	}

	candidates := append([]service.OpenAIAdaptivePoolCandidate(nil), req.Candidates...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		return candidates[i].AccountID < candidates[j].AccountID
	})
	keys := make([]string, 1, 1+(len(candidates)*3))
	keys[0] = openAIAdaptivePoolStateKey(req.Scope)
	args := make([]any, 0, 10+(len(candidates)*4))
	exitStableSeconds := int(req.ExitHighLoadStableFor.Seconds())
	if exitStableSeconds < 0 {
		exitStableSeconds = 0
	}
	args = append(args,
		c.slotTTLSeconds,
		liveLeaseTTLSeconds,
		requestID,
		req.AccountSharePercent,
		req.APIKeySharePercent,
		req.EnterHighLoadPercent,
		req.ExitHighLoadPercent,
		exitStableSeconds,
		openAIAdaptivePoolStateTTLSeconds,
		len(candidates),
	)

	seen := make(map[int64]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.AccountID <= 0 {
			return nil, fmt.Errorf("invalid OpenAI adaptive pool account id %d", candidate.AccountID)
		}
		if _, exists := seen[candidate.AccountID]; exists {
			return nil, fmt.Errorf("duplicate OpenAI adaptive pool account id %d", candidate.AccountID)
		}
		seen[candidate.AccountID] = struct{}{}
		poolCode, err := adaptivePoolCode(candidate.Pool)
		if err != nil {
			return nil, err
		}
		keys = append(keys,
			accountSlotKey(candidate.AccountID),
			liveAccountSlotKey(candidate.AccountID),
			accountWaitKey(candidate.AccountID),
		)
		args = append(args,
			strconv.FormatInt(candidate.AccountID, 10),
			poolCode,
			candidate.Priority,
			candidate.MaxConcurrency,
		)
	}

	raw, err := acquireOpenAIAdaptivePoolSlotScript.Run(ctx, c.rdb, keys, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("acquire OpenAI adaptive pool slot: %w", err)
	}
	acquired, err := redisScriptInt64At(raw, 0)
	if err != nil {
		return nil, err
	}
	accountIDRaw, err := redisScriptStringAt(raw, 1)
	if err != nil {
		return nil, err
	}
	accountID := int64(0)
	if accountIDRaw != "" {
		accountID, err = strconv.ParseInt(accountIDRaw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse OpenAI adaptive pool account id: %w", err)
		}
	}
	preferredPool, err := redisScriptInt64At(raw, 2)
	if err != nil {
		return nil, err
	}
	selectedPool, err := redisScriptInt64At(raw, 3)
	if err != nil {
		return nil, err
	}
	modeCode, err := redisScriptInt64At(raw, 4)
	if err != nil {
		return nil, err
	}
	accountActive, err := redisScriptInt64At(raw, 5)
	if err != nil {
		return nil, err
	}
	keyActive, err := redisScriptInt64At(raw, 6)
	if err != nil {
		return nil, err
	}
	accountCapacity, err := redisScriptInt64At(raw, 7)
	if err != nil {
		return nil, err
	}
	keyCapacity, err := redisScriptInt64At(raw, 8)
	if err != nil {
		return nil, err
	}
	accountWaiting, err := redisScriptInt64At(raw, 9)
	if err != nil {
		return nil, err
	}
	keyWaiting, err := redisScriptInt64At(raw, 10)
	if err != nil {
		return nil, err
	}
	keyBudget, err := redisScriptInt64At(raw, 11)
	if err != nil {
		return nil, err
	}
	fallbackReason, err := redisScriptStringAt(raw, 12)
	if err != nil {
		return nil, err
	}
	budgetReason, err := redisScriptStringAt(raw, 13)
	if err != nil {
		return nil, err
	}
	now, err := redisScriptInt64At(raw, 14)
	if err != nil {
		return nil, err
	}

	mode := service.OpenAIAdaptivePoolModeLow
	if modeCode == 1 {
		mode = service.OpenAIAdaptivePoolModeHigh
	}
	decision := &service.OpenAIAdaptivePoolAcquireDecision{
		Acquired:        acquired == 1,
		AccountID:       accountID,
		PreferredPool:   adaptivePoolName(preferredPool),
		SelectedPool:    adaptivePoolName(selectedPool),
		Mode:            mode,
		AccountActive:   int(accountActive),
		APIKeyActive:    int(keyActive),
		AccountCapacity: int(accountCapacity),
		APIKeyCapacity:  int(keyCapacity),
		AccountWaiting:  int(accountWaiting),
		APIKeyWaiting:   int(keyWaiting),
		APIKeyBudget:    int(keyBudget),
		FallbackReason:  fallbackReason,
		BudgetReason:    budgetReason,
	}
	if decision.Acquired && decision.AccountID > 0 {
		c.touchActiveIndexAt(
			ctx,
			accountActiveIndexKey,
			decision.AccountID,
			now+int64(c.slotTTLSeconds),
		)
	}
	return decision, nil
}

var _ service.OpenAIAdaptivePoolSlotCache = (*concurrencyCache)(nil)
