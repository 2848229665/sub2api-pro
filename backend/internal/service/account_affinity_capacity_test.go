package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type affinityCapacityAccountRepoStub struct {
	AccountRepository
	account          *Account
	accounts         []*Account
	updateExtraCalls int
	bulkUpdateCalls  int
	bulkUpdate       AccountBulkUpdate
}

func (r *affinityCapacityAccountRepoStub) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *affinityCapacityAccountRepoStub) UpdateExtra(context.Context, int64, map[string]any) error {
	r.updateExtraCalls++
	return nil
}

func (r *affinityCapacityAccountRepoStub) GetByIDs(context.Context, []int64) ([]*Account, error) {
	if r.accounts != nil {
		return r.accounts, nil
	}
	return []*Account{r.account}, nil
}

func (r *affinityCapacityAccountRepoStub) BulkUpdate(_ context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	r.bulkUpdateCalls++
	r.bulkUpdate = updates
	return int64(len(ids)), nil
}

func TestAccountAffinityConcurrencyReserveUsesGlobalPercent(t *testing.T) {
	account := &Account{
		Platform:    PlatformOpenAI,
		Concurrency: 10,
		Extra: map[string]any{
			"affinity_concurrency_reserve": 9,
		},
	}

	require.Equal(t, 2, account.GetAffinityConcurrencyReserve(20))
	require.Equal(t, 8, account.GeneralConcurrencyLimit(20))
	require.Equal(t, 10, account.ConcurrencyLimitForAffinity(true, 20))
	require.Equal(t, 8, account.ConcurrencyLimitForAffinity(false, 20))
	require.Zero(t, account.GetAffinityConcurrencyReserve(0))
	require.Equal(t, 10, account.GeneralConcurrencyLimit(0))

	account.Concurrency = 7
	require.Equal(t, 2, account.GetAffinityConcurrencyReserve(35), "reserve uses floor(C*P/100)")
	require.Equal(t, 5, account.GeneralConcurrencyLimit(35))

	account.Concurrency = 0
	require.Zero(t, account.GetAffinityConcurrencyReserve(99))
	require.Zero(t, account.GeneralConcurrencyLimit(99))
}

func TestAccountAffinityConcurrencyReserveInvalidPercentFallsBackToDefault(t *testing.T) {
	account := &Account{Concurrency: 10}

	require.Equal(t, 2, account.GetAffinityConcurrencyReserve(-1))
	require.Equal(t, 2, account.GetAffinityConcurrencyReserve(100))
	require.Equal(t, 2, account.GetAffinityConcurrencyReserve())
}

func TestAdminServiceAcceptsLegacyAffinityReserveExtraWithoutUsingIt(t *testing.T) {
	repo := &affinityCapacityAccountRepoStub{
		account: &Account{ID: 1, Platform: PlatformAnthropic, Concurrency: 1},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 1, map[string]any{
		"affinity_concurrency_reserve": 99,
	}))
	require.Equal(t, 1, repo.updateExtraCalls)
}

func TestAdminServiceBulkUpdateAccountsDoesNotValidateLegacyAffinityReserve(t *testing.T) {
	repo := &affinityCapacityAccountRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2},
		Extra: map[string]any{
			"affinity_concurrency_reserve": 99,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Success)
	require.Equal(t, 1, repo.bulkUpdateCalls)
}

func TestAdminServiceBulkUpdateAccountsPersistsEffectiveConcurrency(t *testing.T) {
	t.Run("all Grok OAuth targets normalize zero to one", func(t *testing.T) {
		repo := &affinityCapacityAccountRepoStub{
			accounts: []*Account{
				{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 2},
				{ID: 2, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 3},
			},
		}
		svc := &adminServiceImpl{accountRepo: repo}
		concurrency := 0

		result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs:  []int64{1, 2},
			Concurrency: &concurrency,
		})
		require.NoError(t, err)
		require.Equal(t, 2, result.Success)
		require.NotNil(t, repo.bulkUpdate.Concurrency)
		require.Equal(t, 1, *repo.bulkUpdate.Concurrency)
	})

	t.Run("mixed targets with different effective values are rejected", func(t *testing.T) {
		repo := &affinityCapacityAccountRepoStub{
			accounts: []*Account{
				{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Concurrency: 2},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 3},
			},
		}
		svc := &adminServiceImpl{accountRepo: repo}
		concurrency := 0

		_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
			AccountIDs:  []int64{1, 2},
			Concurrency: &concurrency,
		})
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		require.Zero(t, repo.bulkUpdateCalls)
	})
}
