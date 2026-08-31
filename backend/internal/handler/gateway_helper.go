package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const gatewayStreamHeartbeatBytesKey = "gateway_stream_heartbeat_bytes"

func recordGatewayStreamHeartbeat(c *gin.Context, written int) {
	if c == nil || written <= 0 {
		return
	}
	total, _ := c.Get(gatewayStreamHeartbeatBytesKey)
	bytes, _ := total.(int)
	c.Set(gatewayStreamHeartbeatBytesKey, bytes+written)
}

func gatewayStreamHasOnlyHeartbeats(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	value, ok := c.Get(gatewayStreamHeartbeatBytesKey)
	if !ok {
		return false
	}
	heartbeatBytes, _ := value.(int)
	return heartbeatBytes > 0 && c.Writer.Size() == heartbeatBytes
}

// claudeCodeValidator is a singleton validator for Claude Code client detection
var claudeCodeValidator = service.NewClaudeCodeValidator()

// SetClaudeCodeClientContext 检查请求是否来自 Claude Code 客户端，并设置到 context 中
// 返回更新后的 context
func SetClaudeCodeClientContext(c *gin.Context, body []byte, parsedReq *service.ParsedRequest) {
	if c == nil || c.Request == nil {
		return
	}
	ua := c.GetHeader("User-Agent")
	// Fast path：非 Claude CLI UA 直接判定 false，避免热路径二次 JSON 反序列化。
	if !claudeCodeValidator.ValidateUserAgent(ua) {
		ctx := service.SetClaudeCodeClient(c.Request.Context(), false)
		c.Request = c.Request.WithContext(ctx)
		return
	}

	isClaudeCode := false
	if !strings.Contains(c.Request.URL.Path, "messages") {
		// 与 Validate 行为一致：非 messages 路径 UA 命中即可视为 Claude Code 客户端。
		isClaudeCode = true
	} else {
		// 仅在确认为 Claude CLI 且 messages 路径时再做 body 解析。
		bodyMap := claudeCodeBodyMapFromParsedRequest(parsedReq)
		if bodyMap == nil && len(body) > 0 {
			_ = json.Unmarshal(body, &bodyMap)
		}
		isClaudeCode = claudeCodeValidator.Validate(c.Request, bodyMap)
	}

	// 更新 request context
	ctx := service.SetClaudeCodeClient(c.Request.Context(), isClaudeCode)

	// 仅在确认为 Claude Code 客户端时提取版本号写入 context
	if isClaudeCode {
		if version := claudeCodeValidator.ExtractVersion(ua); version != "" {
			ctx = service.SetClaudeCodeVersion(ctx, version)
		}
	}

	c.Request = c.Request.WithContext(ctx)
}

func claudeCodeBodyMapFromParsedRequest(parsedReq *service.ParsedRequest) map[string]any {
	if parsedReq == nil {
		return nil
	}
	bodyMap := map[string]any{
		"model": parsedReq.Model,
	}
	if parsedReq.HasSystem {
		if system, ok := parsedReq.SystemValue(); ok {
			bodyMap["system"] = system
		} else {
			bodyMap["system"] = nil
		}
	}
	if parsedReq.MetadataUserID != "" {
		bodyMap["metadata"] = map[string]any{"user_id": parsedReq.MetadataUserID}
	}
	return bodyMap
}

// 并发槽位等待相关常量
//
// 性能优化说明：
// 原实现使用固定间隔（100ms）轮询并发槽位，存在以下问题：
// 1. 高并发时频繁轮询增加 Redis 压力
// 2. 固定间隔可能导致多个请求同时重试（惊群效应）
//
// 新实现使用指数退避 + 抖动算法：
// 1. 初始退避 100ms，每次乘以 1.5，最大 2s
// 2. 添加 ±20% 的随机抖动，分散重试时间点
// 3. 减少 Redis 压力，避免惊群效应
const (
	// maxConcurrencyWait 等待并发槽位的最大时间
	maxConcurrencyWait = 30 * time.Second
	// defaultPingInterval 流式响应等待时发送 ping 的默认间隔
	defaultPingInterval = 10 * time.Second
	// initialBackoff 初始退避时间
	initialBackoff = 100 * time.Millisecond
	// backoffMultiplier 退避时间乘数（指数退避）
	backoffMultiplier = 1.5
	// maxBackoff 最大退避时间
	maxBackoff = 2 * time.Second
)

// SSEPingFormat defines the format of SSE ping events for different platforms
type SSEPingFormat string

const (
	// SSEPingFormatClaude is the Claude/Anthropic SSE ping format
	SSEPingFormatClaude SSEPingFormat = "data: {\"type\": \"ping\"}\n\n"
	// SSEPingFormatNone indicates no ping should be sent (e.g., OpenAI has no ping spec)
	SSEPingFormatNone SSEPingFormat = ""
	// SSEPingFormatComment is an SSE comment ping for OpenAI/Codex CLI clients
	SSEPingFormatComment SSEPingFormat = ":\n\n"
)

// ConcurrencyError represents a concurrency limit error with context
type ConcurrencyError struct {
	SlotType  string
	IsTimeout bool
}

func (e *ConcurrencyError) Error() string {
	if e.IsTimeout {
		return fmt.Sprintf("timeout waiting for %s concurrency slot", e.SlotType)
	}
	return fmt.Sprintf("%s concurrency limit reached", e.SlotType)
}

type WaitQueueFullError struct {
	SlotType string
}

func (e *WaitQueueFullError) Error() string {
	return "Too many pending requests, please retry later"
}

// ConcurrencyHelper provides common concurrency slot management for gateway handlers
type ConcurrencyHelper struct {
	concurrencyService *service.ConcurrencyService
	pingFormat         SSEPingFormat
	pingInterval       time.Duration
}

type slotLoadSnapshot struct {
	ActiveCount  int
	WaitingCount int
	Available    bool
	Err          error
}

const slotLoadSnapshotTimeout = 500 * time.Millisecond

func (s slotLoadSnapshot) zapFields() []zap.Field {
	fields := []zap.Field{
		zap.Bool("load_snapshot_available", s.Available),
	}
	if s.Available {
		fields = append(fields,
			zap.Int("active_count", s.ActiveCount),
			zap.Int("waiting_count", s.WaitingCount),
		)
	}
	if s.Err != nil {
		fields = append(fields, zap.String("load_snapshot_error", s.Err.Error()))
	}
	return fields
}

// NewConcurrencyHelper creates a new ConcurrencyHelper
func NewConcurrencyHelper(concurrencyService *service.ConcurrencyService, pingFormat SSEPingFormat, pingInterval time.Duration) *ConcurrencyHelper {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &ConcurrencyHelper{
		concurrencyService: concurrencyService,
		pingFormat:         pingFormat,
		pingInterval:       pingInterval,
	}
}

func (h *ConcurrencyHelper) accountLoadSnapshot(ctx context.Context, accountID int64, maxConcurrency int) slotLoadSnapshot {
	if h == nil || h.concurrencyService == nil || accountID <= 0 {
		return slotLoadSnapshot{}
	}
	loadMap, err := h.concurrencyService.GetAccountsLoadBatchFreshWithTimeout(
		ctx,
		[]service.AccountWithConcurrency{{
			ID:             accountID,
			MaxConcurrency: maxConcurrency,
		}},
		slotLoadSnapshotTimeout,
	)
	if err != nil {
		return slotLoadSnapshot{Err: err}
	}
	load := loadMap[accountID]
	if load == nil {
		return slotLoadSnapshot{}
	}
	return slotLoadSnapshot{
		ActiveCount:  load.CurrentConcurrency,
		WaitingCount: load.WaitingCount,
		Available:    true,
	}
}

func (h *ConcurrencyHelper) userLoadSnapshot(ctx context.Context, userID int64, maxConcurrency int) slotLoadSnapshot {
	if h == nil || h.concurrencyService == nil || userID <= 0 {
		return slotLoadSnapshot{}
	}
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	snapshotCtx, cancel := context.WithTimeout(baseCtx, slotLoadSnapshotTimeout)
	defer cancel()
	loadMap, err := h.concurrencyService.GetUsersLoadBatch(snapshotCtx, []service.UserWithConcurrency{{
		ID:             userID,
		MaxConcurrency: maxConcurrency,
	}})
	if err != nil {
		return slotLoadSnapshot{Err: err}
	}
	load := loadMap[userID]
	if load == nil {
		return slotLoadSnapshot{}
	}
	return slotLoadSnapshot{
		ActiveCount:  load.CurrentConcurrency,
		WaitingCount: load.WaitingCount,
		Available:    true,
	}
}

// wrapReleaseOnDone ensures release runs at most once and still triggers on context cancellation.
// 用于避免客户端断开或上游超时导致的并发槽位泄漏。
// 优化：基于 context.AfterFunc 注册回调，避免每请求额外守护 goroutine。
func wrapReleaseOnDone(ctx context.Context, releaseFunc func()) func() {
	if releaseFunc == nil {
		return nil
	}
	var once sync.Once
	releaseOnce := func() {
		once.Do(releaseFunc)
	}
	stop := context.AfterFunc(ctx, releaseOnce)

	return func() {
		_ = stop()
		releaseOnce()
	}
}

// IncrementWaitCount increments the wait count for a user
func (h *ConcurrencyHelper) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementWaitCount(ctx, userID, maxWait)
}

// DecrementWaitCount decrements the wait count for a user
func (h *ConcurrencyHelper) DecrementWaitCount(ctx context.Context, userID int64) {
	h.concurrencyService.DecrementWaitCount(ctx, userID)
}

// IncrementAccountWaitCount increments the wait count for an account
func (h *ConcurrencyHelper) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return h.concurrencyService.IncrementAccountWaitCount(ctx, accountID, maxWait)
}

// DecrementAccountWaitCount decrements the wait count for an account
func (h *ConcurrencyHelper) DecrementAccountWaitCount(ctx context.Context, accountID int64) {
	h.concurrencyService.DecrementAccountWaitCount(ctx, accountID)
}

// TryAcquireUserSlot 尝试立即获取用户并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

func (h *ConcurrencyHelper) TryAcquireUserSlotForAPIKey(ctx context.Context, userID int64, maxConcurrency int, apiKeyID int64) (func(), bool, error) {
	releaseFunc, acquired, err := h.TryAcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil || !acquired {
		return releaseFunc, acquired, err
	}
	return h.withAPIKeySlot(ctx, apiKeyID, releaseFunc), true, nil
}

// AcquireOpenAIWSIngressLease bounds the whole client WebSocket lifecycle,
// independently from per-turn user and account slots.
func (h *ConcurrencyHelper) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int) (*service.OpenAIWSIngressLease, bool, error) {
	if h == nil || h.concurrencyService == nil {
		return nil, false, fmt.Errorf("concurrency service is unavailable")
	}
	return h.concurrencyService.AcquireOpenAIWSIngressLease(ctx, apiKeyID, maxConnections)
}

// TryAcquireAccountSlot 尝试立即获取账号并发槽位。
// 返回值: (releaseFunc, acquired, error)
func (h *ConcurrencyHelper) TryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (func(), bool, error) {
	result, err := h.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, false, err
	}
	if !result.Acquired {
		return nil, false, nil
	}
	return result.ReleaseFunc, true, nil
}

// AcquireUserSlotWithWait acquires a user concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireUserSlotWithWait(c *gin.Context, userID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.acquireUserSlotWithWaitTimeout(c, userID, maxConcurrency, maxConcurrencyWait, isStream, streamStarted)
}

// AcquireUserSlotWithWaitObserved adds request-scoped lifecycle logs only when
// the immediate user slot acquisition fails and the request enters (or is
// rejected by) the wait queue. eventPrefix should identify the gateway, for
// example "openai".
func (h *ConcurrencyHelper) AcquireUserSlotWithWaitObserved(
	c *gin.Context,
	userID int64,
	maxConcurrency int,
	isStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
	eventPrefix string,
) (func(), error) {
	return h.acquireUserSlotWithWaitTimeoutObserved(
		c,
		userID,
		maxConcurrency,
		maxConcurrencyWait,
		isStream,
		streamStarted,
		reqLog,
		eventPrefix,
	)
}

func (h *ConcurrencyHelper) acquireUserSlotWithWaitTimeout(c *gin.Context, userID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.acquireUserSlotWithWaitTimeoutObserved(c, userID, maxConcurrency, timeout, isStream, streamStarted, nil, "")
}

func (h *ConcurrencyHelper) acquireUserSlotWithWaitTimeoutObserved(
	c *gin.Context,
	userID int64,
	maxConcurrency int,
	timeout time.Duration,
	isStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
	eventPrefix string,
) (func(), error) {
	ctx := c.Request.Context()
	eventPrefix = strings.TrimSpace(eventPrefix)

	// Try to acquire immediately
	releaseFunc, acquired, err := h.TryAcquireUserSlot(ctx, userID, maxConcurrency)
	if err != nil {
		logUserSlotAcquireFailure(reqLog, eventPrefix, userID, maxConcurrency, 0, timeout, 0, "quick_acquire", streamStartedValue(streamStarted), slotLoadSnapshot{}, err)
		return nil, err
	}

	if acquired {
		return h.withAPIKeySlotFromGin(c, releaseFunc), nil
	}

	queueLimit := service.CalculateMaxWait(maxConcurrency) - maxConcurrency
	if queueLimit < 1 {
		queueLimit = 1
	}
	waitStartedAt := time.Now()
	canWait, err := h.IncrementWaitCount(ctx, userID, queueLimit)
	if err != nil {
		logUserSlotAcquireFailure(reqLog, eventPrefix, userID, maxConcurrency, queueLimit, timeout, time.Since(waitStartedAt), "queue_increment", streamStartedValue(streamStarted), slotLoadSnapshot{}, err)
		return nil, err
	}
	if !canWait {
		queueErr := &WaitQueueFullError{SlotType: "user"}
		snapshot := slotLoadSnapshot{}
		if reqLog != nil && eventPrefix != "" {
			snapshot = h.userLoadSnapshot(ctx, userID, maxConcurrency)
			fields := userSlotWaitLogFields(userID, maxConcurrency, queueLimit, timeout, time.Since(waitStartedAt), "queue_full", streamStartedValue(streamStarted), snapshot)
			fields = append(fields, zap.Bool("terminal_event", false))
			reqLog.Warn(eventPrefix+".user_wait_queue_full", fields...)
		}
		logUserSlotAcquireFailure(reqLog, eventPrefix, userID, maxConcurrency, queueLimit, timeout, time.Since(waitStartedAt), "queue_full", streamStartedValue(streamStarted), snapshot, queueErr)
		return nil, queueErr
	}
	waitCounted := true
	decrementWaitCount := func() {
		if !waitCounted {
			return
		}
		h.DecrementWaitCount(ctx, userID)
		waitCounted = false
	}
	defer decrementWaitCount()

	if reqLog != nil && eventPrefix != "" {
		snapshot := h.userLoadSnapshot(ctx, userID, maxConcurrency)
		fields := userSlotWaitLogFields(userID, maxConcurrency, queueLimit, timeout, 0, "waiting", streamStartedValue(streamStarted), snapshot)
		reqLog.Info(eventPrefix+".user_wait_started", fields...)
	}

	// Need to wait - handle streaming ping if needed
	releaseFunc, err = h.waitForSlotWithPingTimeout(c, "user", userID, maxConcurrency, timeout, isStream, streamStarted, false)
	decrementWaitCount()
	waitDuration := time.Since(waitStartedAt)
	snapshot := slotLoadSnapshot{}
	if reqLog != nil && eventPrefix != "" {
		snapshot = h.userLoadSnapshot(ctx, userID, maxConcurrency)
	}
	if err != nil {
		logUserSlotAcquireFailure(reqLog, eventPrefix, userID, maxConcurrency, queueLimit, timeout, waitDuration, "wait", streamStartedValue(streamStarted), snapshot, err)
		return nil, err
	}
	if reqLog != nil && eventPrefix != "" {
		fields := userSlotWaitLogFields(userID, maxConcurrency, queueLimit, timeout, waitDuration, "acquired", streamStartedValue(streamStarted), snapshot)
		reqLog.Info(eventPrefix+".user_wait_acquired", fields...)
	}
	return h.withAPIKeySlotFromGin(c, releaseFunc), nil
}

func userSlotWaitLogFields(
	userID int64,
	maxConcurrency int,
	queueLimit int,
	timeout time.Duration,
	waitDuration time.Duration,
	phase string,
	streamStarted bool,
	snapshot slotLoadSnapshot,
) []zap.Field {
	fields := []zap.Field{
		zap.Int64("user_id", userID),
		zap.Int("max_concurrency", maxConcurrency),
		zap.Int("queue_limit", queueLimit),
		zap.Int64("timeout_ms", timeout.Milliseconds()),
		zap.String("phase", phase),
		zap.Bool("stream_started", streamStarted),
	}
	if waitDuration > 0 {
		fields = append(fields, zap.Int64("wait_ms", waitDuration.Milliseconds()))
	}
	return append(fields, snapshot.zapFields()...)
}

func logUserSlotAcquireFailure(
	reqLog *zap.Logger,
	eventPrefix string,
	userID int64,
	maxConcurrency int,
	queueLimit int,
	timeout time.Duration,
	waitDuration time.Duration,
	phase string,
	streamStarted bool,
	snapshot slotLoadSnapshot,
	err error,
) {
	if reqLog == nil || eventPrefix == "" {
		return
	}
	fields := userSlotWaitLogFields(userID, maxConcurrency, queueLimit, timeout, waitDuration, phase, streamStarted, snapshot)
	fields = append(fields,
		zap.String("failure_reason", concurrencyWaitFailureReason(err)),
	)
	fields = append(fields, concurrencyResponseLogFields(err, "user")...)
	fields = append(fields, zap.Error(err))
	reqLog.Warn(eventPrefix+".user_slot_acquire_failed", fields...)
}

func streamStartedValue(streamStarted *bool) bool {
	return streamStarted != nil && *streamStarted
}

func concurrencyWaitFailureReason(err error) string {
	var queueFullErr *WaitQueueFullError
	if errors.As(err, &queueFullErr) {
		return "wait_queue_full"
	}
	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) && concurrencyErr.IsTimeout {
		return "wait_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "request_deadline_exceeded"
	}
	return "acquire_error"
}

func concurrencyResponseLogFields(err error, slotType string) []zap.Field {
	status, errType, _ := concurrencyErrorResponse(err, slotType)
	return []zap.Field{
		zap.Bool("terminal_event", true),
		zap.Int("mapped_status_code", status),
		zap.String("mapped_error_type", errType),
	}
}

func (h *ConcurrencyHelper) withAPIKeySlotFromGin(c *gin.Context, releaseFunc func()) func() {
	if c == nil {
		return releaseFunc
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return releaseFunc
	}
	return h.withAPIKeySlot(c.Request.Context(), apiKey.ID, releaseFunc)
}

func (h *ConcurrencyHelper) withAPIKeySlot(ctx context.Context, apiKeyID int64, releaseFunc func()) func() {
	if h == nil || h.concurrencyService == nil || apiKeyID <= 0 {
		return releaseFunc
	}
	apiKeyReleaseFunc := h.concurrencyService.TrackAPIKeySlot(ctx, apiKeyID)
	return func() {
		if releaseFunc != nil {
			releaseFunc()
		}
		if apiKeyReleaseFunc != nil {
			apiKeyReleaseFunc()
		}
	}
}

// AcquireAccountSlotWithWait acquires an account concurrency slot, waiting if necessary.
// For streaming requests, sends ping events during the wait.
// streamStarted is updated if streaming response has begun.
func (h *ConcurrencyHelper) AcquireAccountSlotWithWait(c *gin.Context, accountID int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	ctx := c.Request.Context()

	// Try to acquire immediately
	releaseFunc, acquired, err := h.TryAcquireAccountSlot(ctx, accountID, maxConcurrency)
	if err != nil {
		return nil, err
	}

	if acquired {
		return releaseFunc, nil
	}

	// Need to wait - handle streaming ping if needed
	return h.waitForSlotWithPing(c, "account", accountID, maxConcurrency, isStream, streamStarted)
}

// waitForSlotWithPing waits for a concurrency slot, sending ping events for streaming requests.
// streamStarted pointer is updated when streaming begins (for proper error handling by caller).
func (h *ConcurrencyHelper) waitForSlotWithPing(c *gin.Context, slotType string, id int64, maxConcurrency int, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, slotType, id, maxConcurrency, maxConcurrencyWait, isStream, streamStarted, false)
}

// waitForSlotWithPingTimeout waits for a concurrency slot with a custom timeout.
func (h *ConcurrencyHelper) waitForSlotWithPingTimeout(c *gin.Context, slotType string, id int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool, tryImmediate bool) (func(), error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	acquireSlot := func() (*service.AcquireResult, error) {
		if slotType == "user" {
			return h.concurrencyService.AcquireUserSlot(ctx, id, maxConcurrency)
		}
		return h.concurrencyService.AcquireAccountSlot(ctx, id, maxConcurrency)
	}

	if tryImmediate {
		result, err := acquireSlot()
		if err != nil {
			return nil, err
		}
		if result.Acquired {
			return result.ReleaseFunc, nil
		}
	}

	// Determine if ping is needed (streaming + ping format defined)
	needPing := isStream && h.pingFormat != ""

	var flusher http.Flusher
	if needPing {
		var ok bool
		flusher, ok = c.Writer.(http.Flusher)
		if !ok {
			return nil, fmt.Errorf("streaming not supported")
		}
	}

	// Only create ping ticker if ping is needed
	var pingCh <-chan time.Time
	if needPing {
		pingTicker := time.NewTicker(h.pingInterval)
		defer pingTicker.Stop()
		pingCh = pingTicker.C
	}

	backoff := initialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			if parentErr := c.Request.Context().Err(); parentErr != nil {
				return nil, parentErr
			}
			return nil, &ConcurrencyError{
				SlotType:  slotType,
				IsTimeout: true,
			}

		case <-pingCh:
			// Send ping to keep connection alive
			if !*streamStarted {
				c.Header("Content-Type", "text/event-stream")
				c.Header("Cache-Control", "no-cache")
				c.Header("Connection", "keep-alive")
				c.Header("X-Accel-Buffering", "no")
				*streamStarted = true
			}
			written, err := fmt.Fprint(c.Writer, string(h.pingFormat))
			if err != nil {
				return nil, err
			}
			recordGatewayStreamHeartbeat(c, written)
			flusher.Flush()

		case <-timer.C:
			// Try to acquire slot
			result, err := acquireSlot()
			if err != nil {
				return nil, err
			}

			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = nextBackoff(backoff)
			timer.Reset(backoff)
		}
	}
}

// AcquireAccountSlotWithWaitTimeout acquires an account slot with a custom timeout (keeps SSE ping).
func (h *ConcurrencyHelper) AcquireAccountSlotWithWaitTimeout(c *gin.Context, accountID int64, maxConcurrency int, timeout time.Duration, isStream bool, streamStarted *bool) (func(), error) {
	return h.waitForSlotWithPingTimeout(c, "account", accountID, maxConcurrency, timeout, isStream, streamStarted, true)
}

// nextBackoff 计算下一次退避时间
// 性能优化：使用指数退避 + 随机抖动，避免惊群效应
// current: 当前退避时间
// 返回值：下一次退避时间（100ms ~ 2s 之间）
func nextBackoff(current time.Duration) time.Duration {
	// 指数退避：当前时间 * 1.5
	next := time.Duration(float64(current) * backoffMultiplier)
	if next > maxBackoff {
		next = maxBackoff
	}
	// 添加 ±20% 的随机抖动（jitter 范围 0.8 ~ 1.2）
	// 抖动可以分散多个请求的重试时间点，避免同时冲击 Redis
	jitter := 0.8 + rand.Float64()*0.4
	jittered := time.Duration(float64(next) * jitter)
	if jittered < initialBackoff {
		return initialBackoff
	}
	if jittered > maxBackoff {
		return maxBackoff
	}
	return jittered
}
