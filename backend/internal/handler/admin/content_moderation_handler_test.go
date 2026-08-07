package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cyberRequestAuditHandlerRepo struct {
	audit       *service.CyberPolicyRequestAudit
	err         error
	calls       int
	listFilter  service.ContentModerationLogFilter
	listCalls   int
	stats       *service.ContentModerationKeywordStats
	statsFilter service.ContentModerationKeywordStatsFilter
	statsCalls  int
}

func (r *cyberRequestAuditHandlerRepo) CreateLog(context.Context, *service.ContentModerationLog) error {
	return nil
}

func (r *cyberRequestAuditHandlerRepo) ListLogs(_ context.Context, filter service.ContentModerationLogFilter) ([]service.ContentModerationLog, *pagination.PaginationResult, error) {
	r.listCalls++
	r.listFilter = filter
	return []service.ContentModerationLog{}, &pagination.PaginationResult{Page: filter.Pagination.Page, PageSize: filter.Pagination.PageSize}, nil
}

func (r *cyberRequestAuditHandlerRepo) GetKeywordHitStats(_ context.Context, filter service.ContentModerationKeywordStatsFilter) (*service.ContentModerationKeywordStats, error) {
	r.statsCalls++
	r.statsFilter = filter
	if r.stats != nil {
		return r.stats, r.err
	}
	return &service.ContentModerationKeywordStats{
		Users: service.ContentModerationUserHitCountPage{
			Items:    []service.ContentModerationUserHitCount{},
			Page:     filter.UserPagination.Page,
			PageSize: filter.UserPagination.PageSize,
		},
		Keywords: service.ContentModerationKeywordHitCountPage{
			Items:    []service.ContentModerationKeywordHitCount{},
			Page:     filter.KeywordPagination.Page,
			PageSize: filter.KeywordPagination.PageSize,
		},
	}, nil
}

func (r *cyberRequestAuditHandlerRepo) GetCyberPolicyRequestAudit(context.Context, int64) (*service.CyberPolicyRequestAudit, error) {
	r.calls++
	return r.audit, r.err
}

func (r *cyberRequestAuditHandlerRepo) CountFlaggedByUserSince(context.Context, int64, time.Time, bool) (int, error) {
	return 0, nil
}

func (r *cyberRequestAuditHandlerRepo) CleanupExpiredLogs(context.Context, time.Time, time.Time) (*service.ContentModerationCleanupResult, error) {
	return &service.ContentModerationCleanupResult{}, nil
}

func (r *cyberRequestAuditHandlerRepo) UpdateLogEmailSent(context.Context, int64, bool) error {
	return nil
}

func (r *cyberRequestAuditHandlerRepo) UpdateCyberPolicyOutcome(context.Context, int64, int, bool, bool) error {
	return nil
}

func TestContentModerationHandlerListLogsPassesValidAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cyberRequestAuditHandlerRepo{}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs", handler.ListLogs)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs?action=keyword_block&page=2&page_size=15", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, repo.listCalls)
	require.Equal(t, service.ContentModerationActionKeywordBlock, repo.listFilter.Action)
	require.Equal(t, 2, repo.listFilter.Pagination.Page)
	require.Equal(t, 15, repo.listFilter.Pagination.PageSize)
}

func TestContentModerationHandlerListLogsRejectsInvalidAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cyberRequestAuditHandlerRepo{}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs", handler.ListLogs)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs?action=BLOCK", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, repo.listCalls)
	var response struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, "Invalid action", response.Message)
}

func TestContentModerationHandlerGetKeywordHitStatsPassesDateRangeAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lastHitAt := time.Date(2026, time.August, 7, 8, 30, 0, 0, time.UTC)
	repo := &cyberRequestAuditHandlerRepo{stats: &service.ContentModerationKeywordStats{
		TotalHits:    9,
		UserCount:    2,
		KeywordCount: 3,
		Users: service.ContentModerationUserHitCountPage{
			Items: []service.ContentModerationUserHitCount{{
				Username:  "alice",
				UserEmail: "alice@example.com",
				HitCount:  6,
				LastHitAt: lastHitAt,
			}},
			Total:    2,
			Page:     2,
			PageSize: 15,
			Pages:    1,
		},
		Keywords: service.ContentModerationKeywordHitCountPage{
			Items:    []service.ContentModerationKeywordHitCount{},
			Total:    3,
			Page:     3,
			PageSize: 10,
			Pages:    1,
		},
	}}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/keyword-stats", handler.GetKeywordHitStats)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/keyword-stats?from=2026-08-01&to=2026-08-07&user_page=2&user_page_size=15&keyword_page=3&keyword_page_size=10", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, repo.statsCalls)
	require.Equal(t, 2, repo.statsFilter.UserPagination.Page)
	require.Equal(t, 15, repo.statsFilter.UserPagination.PageSize)
	require.Equal(t, 3, repo.statsFilter.KeywordPagination.Page)
	require.Equal(t, 10, repo.statsFilter.KeywordPagination.PageSize)
	require.Equal(t, time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC), *repo.statsFilter.From)
	require.Equal(t, time.Date(2026, time.August, 7, 23, 59, 59, int(time.Second-time.Nanosecond), time.UTC), *repo.statsFilter.To)

	var response struct {
		Code int                                   `json:"code"`
		Data service.ContentModerationKeywordStats `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.Equal(t, int64(9), response.Data.TotalHits)
	require.Equal(t, "alice@example.com", response.Data.Users.Items[0].UserEmail)
}

func TestContentModerationHandlerGetKeywordHitStatsRejectsInvalidPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cyberRequestAuditHandlerRepo{}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/keyword-stats", handler.GetKeywordHitStats)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/keyword-stats?user_page=0", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, repo.statsCalls)
}

func TestContentModerationHandlerGetCyberPolicyRequestAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestBody = `{"model":"gpt-5","input":[{"role":"user","content":"audit"},{"type":"function_call_output","output":"complete tool output"}]}`
	repo := &cyberRequestAuditHandlerRepo{audit: &service.CyberPolicyRequestAudit{
		LogID:         77,
		RequestID:     "req-77",
		Protocol:      service.ContentModerationProtocolOpenAIResponses,
		RequestBody:   requestBody,
		OriginalBytes: int64(len(requestBody)),
		StoredBytes:   len(requestBody),
		Truncated:     false,
	}}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs/:id/cyber-request", handler.GetCyberPolicyRequestAudit)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs/77/cyber-request", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Code int                             `json:"code"`
		Data service.CyberPolicyRequestAudit `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Zero(t, response.Code)
	require.Equal(t, *repo.audit, response.Data)
	require.Equal(t, 1, repo.calls)
}

func TestContentModerationHandlerGetCyberPolicyRequestAuditRejectsInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &cyberRequestAuditHandlerRepo{}
	handler := NewContentModerationHandler(service.NewContentModerationService(nil, repo, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.GET("/logs/:id/cyber-request", handler.GetCyberPolicyRequestAudit)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/logs/not-a-number/cyber-request", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, repo.calls)
}
