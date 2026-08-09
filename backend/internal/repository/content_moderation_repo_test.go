package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildContentModerationLogWhere_BlockedIncludesAllBlockActions(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{Result: "blocked"})

	require.Empty(t, args)
	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action IN ('block', 'keyword_block', 'hash_block', 'cyber_policy')")
	require.NotContains(t, sql, "l.action = 'block'")
}

func TestBuildContentModerationLogWhere_ActionUsesParameterizedCondition(t *testing.T) {
	groupID := int64(42)
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{
		Action:   service.ContentModerationActionKeywordBlock,
		GroupID:  &groupID,
		Endpoint: "/v1/responses",
		Search:   "needle",
	})

	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.action = $1")
	require.Contains(t, sql, "l.group_id = $2")
	require.Contains(t, sql, "l.endpoint = $3")
	require.Contains(t, sql, "l.request_id ILIKE $4")
	require.NotContains(t, sql, "l.input_excerpt ILIKE")
	require.Equal(t, []any{
		service.ContentModerationActionKeywordBlock,
		groupID,
		"/v1/responses",
		"%needle%", "%needle%", "%needle%", "%needle%",
	}, args)
}

func TestBuildContentModerationLogWhere_EmailSearchUsesExactMatch(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{
		Result: "hit",
		Action: service.ContentModerationActionKeywordBlock,
		Search: "user@example.com",
	})

	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.user_email = $2")
	require.NotContains(t, sql, "ILIKE")
	require.Equal(t, []any{
		service.ContentModerationActionKeywordBlock,
		"user@example.com",
	}, args)
}

func TestBuildContentModerationLogWhere_UserEmailFilter(t *testing.T) {
	where, args := buildContentModerationLogWhere(service.ContentModerationLogFilter{
		Result:    "hit",
		UserEmail: "alice@example.com",
	})

	sql := strings.Join(where, " AND ")
	require.Contains(t, sql, "l.flagged = TRUE")
	require.Contains(t, sql, "l.user_email = $1")
	require.Equal(t, []any{"alice@example.com"}, args)
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesHashBlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND action <> 'hash_block'")).
		WithArgs(int64(1001), since, false).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, false)

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryCountFlaggedByUserSince_ExcludesCyberPolicyWhenRequested(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	since := time.Now().Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("AND ($3::bool IS FALSE OR action <> 'cyber_policy')")).
		WithArgs(int64(1001), since, true).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))

	count, err := repo.CountFlaggedByUserSince(context.Background(), 1001, since, true)

	require.NoError(t, err)
	require.Equal(t, 3, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetCyberPolicyRequestAudit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	mock.ExpectQuery("SELECT id, request_id, cyber_request_protocol").
		WithArgs(int64(77)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "request_id", "cyber_request_protocol", "cyber_request_snapshot",
			"cyber_request_original_bytes", "cyber_request_stored_bytes", "cyber_request_truncated",
		}).AddRow(77, "req-77", "openai_responses", `{"input":"audit"}`, 128, 17, true))

	item, err := repo.GetCyberPolicyRequestAudit(context.Background(), 77)

	require.NoError(t, err)
	require.Equal(t, int64(77), item.LogID)
	require.Equal(t, "req-77", item.RequestID)
	require.Equal(t, "openai_responses", item.Protocol)
	require.True(t, item.Truncated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetCyberPolicyRequestAudit_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := NewContentModerationRepository(db)
	mock.ExpectQuery("SELECT id, request_id, cyber_request_protocol").
		WithArgs(int64(88)).
		WillReturnError(sql.ErrNoRows)

	item, err := repo.GetCyberPolicyRequestAudit(context.Background(), 88)

	require.Nil(t, item)
	require.ErrorIs(t, err, service.ErrCyberPolicyRequestAuditNotFound)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestContentModerationRepositoryGetKeywordHitStats(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	from := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 7, 23, 59, 59, 0, time.UTC)
	lastUserHit := time.Date(2026, time.August, 7, 8, 30, 0, 0, time.UTC)
	lastKeywordHit := time.Date(2026, time.August, 7, 9, 0, 0, 0, time.UTC)
	repo := NewContentModerationRepository(db)

	mock.ExpectQuery(`(?s)COUNT\(DISTINCT LOWER\(BTRIM\(l\.matched_keyword\)\)\).*FROM content_moderation_logs l`).
		WithArgs(from, to).
		WillReturnRows(sqlmock.NewRows([]string{"total_hits", "user_count", "keyword_count"}).AddRow(12, 2, 3))
	mock.ExpectQuery(`(?s)FROM \(.*COUNT\(DISTINCT LOWER\(BTRIM\(l\.matched_keyword\)\)\).*LEFT JOIN users u`).
		WithArgs(from, to, 10, 10).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "username", "user_email", "hit_count", "keyword_count", "last_hit_at",
		}).AddRow(int64(42), "alice", "alice@example.com", 7, 2, lastUserHit))
	mock.ExpectQuery(`(?s)MIN\(BTRIM\(l\.matched_keyword\)\).*GROUP BY LOWER\(BTRIM\(l\.matched_keyword\)\)`).
		WithArgs(from, to, 5, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"keyword", "hit_count", "user_count", "last_hit_at",
		}).AddRow("secret-token", 8, 2, lastKeywordHit))

	stats, err := repo.GetKeywordHitStats(context.Background(), service.ContentModerationKeywordStatsFilter{
		UserPagination: pagination.PaginationParams{
			Page:     2,
			PageSize: 10,
		},
		KeywordPagination: pagination.PaginationParams{
			Page:     1,
			PageSize: 5,
		},
		From: &from,
		To:   &to,
	})

	require.NoError(t, err)
	require.Equal(t, int64(12), stats.TotalHits)
	require.Equal(t, int64(2), stats.UserCount)
	require.Equal(t, int64(3), stats.KeywordCount)
	require.Equal(t, int64(42), *stats.Users.Items[0].UserID)
	require.Equal(t, "alice", stats.Users.Items[0].Username)
	require.Equal(t, int64(7), stats.Users.Items[0].HitCount)
	require.Equal(t, 2, stats.Users.Page)
	require.Equal(t, int64(3), stats.Keywords.Total)
	require.Equal(t, "secret-token", stats.Keywords.Items[0].Keyword)
	require.Equal(t, int64(8), stats.Keywords.Items[0].HitCount)
	require.NoError(t, mock.ExpectationsWereMet())
}
