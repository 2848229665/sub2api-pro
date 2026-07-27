package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type codexMemoriesFailoverAccountRepo struct {
	service.AccountRepository
	accounts []service.Account
}

func (r codexMemoriesFailoverAccountRepo) GetByID(_ context.Context, id int64) (*service.Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, service.ErrNoAvailableAccounts
}

func (r codexMemoriesFailoverAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r codexMemoriesFailoverAccountRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r codexMemoriesFailoverAccountRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]service.Account, error) {
	return r.accountsForPlatform(platform), nil
}

func (r codexMemoriesFailoverAccountRepo) accountsForPlatform(platform string) []service.Account {
	out := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform {
			out = append(out, account)
		}
	}
	return out
}

type codexMemoriesFailoverHTTPUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
}

func (u *codexMemoriesFailoverHTTPUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(`{"error":{"message":"endpoint unavailable"}}`)),
	}, nil
}

func (u *codexMemoriesFailoverHTTPUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestOpenAIGatewayHandlerCodexMemoriesFailsOverAcrossOAuthAccountsAndSkipsAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(4401)
	accounts := []service.Account{
		{
			ID:          1,
			Name:        "oauth-1",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Priority:    0,
			Credentials: map[string]any{"access_token": "oauth-token-1"},
		},
		{
			ID:          2,
			Name:        "api-key-must-be-skipped",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeAPIKey,
			Status:      service.StatusActive,
			Schedulable: true,
			Priority:    1,
			Credentials: map[string]any{"api_key": "sk-test"},
		},
		{
			ID:          3,
			Name:        "oauth-2",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Status:      service.StatusActive,
			Schedulable: true,
			Priority:    2,
			Credentials: map[string]any{"access_token": "oauth-token-2"},
		},
	}
	repo := codexMemoriesFailoverAccountRepo{accounts: accounts}
	upstream := &codexMemoriesFailoverHTTPUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	gatewayService := service.NewOpenAIGatewayService(
		repo,
		nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		upstream,
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	billingService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingService.Stop)
	handler := NewOpenAIGatewayHandler(
		gatewayService,
		service.NewConcurrencyService(nil),
		billingService,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil,
		cfg,
	)
	handler.maxAccountSwitches = 10

	body := []byte(`{"model":"gpt-5.6-sol","traces":[{"items":[]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/memories/trace_summarize", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
		User:    &service.User{ID: 100},
	})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})

	handler.CodexMemories(c)

	require.Equal(t, []int64{1, 3}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.NotContains(t, rec.Body.String(), "endpoint unavailable")
}
