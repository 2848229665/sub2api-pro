import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

import type { DashboardStats } from '@/types'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUsageTrend, getUserUsageTrend, getUserSpendingRanking } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUsageTrend: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUsageTrend,
      getUserUsageTrend,
      getUserSpendingRanking
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: ref('en')
    })
  }
})

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  total_account_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  today_account_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
})

const mountDashboard = () =>
  mount(DashboardView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        LoadingSpinner: true,
        Icon: true,
        DateRangePicker: true,
        Select: true,
        ModelDistributionChart: true,
        TokenUsageTrend: true,
        Line: true
      }
    }
  })

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUsageTrend.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'day'
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('uses last 24 hours as default dashboard range', async () => {
    mountDashboard()

    await flushPromises()

    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))
  })

  it('shows natural period usage and applies a period as the chart filter', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 7, 8, 12, 0, 0))
    getUsageTrend.mockResolvedValue({
      trend: [
        { date: '2026-08-02', requests: 2, total_tokens: 20, actual_cost: 0.2 },
        { date: '2026-08-03', requests: 3, total_tokens: 30, actual_cost: 0.3 },
        { date: '2026-08-07', requests: 7, total_tokens: 70, actual_cost: 0.7 },
        { date: '2026-08-08', requests: 8, total_tokens: 80, actual_cost: 0.8 }
      ],
      start_date: '2026-07-27',
      end_date: '2026-08-08',
      granularity: 'day'
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(getUsageTrend).toHaveBeenCalledWith({
      start_date: '2026-07-27',
      end_date: '2026-08-08',
      granularity: 'day'
    })
    expect(wrapper.get('[data-testid="period-usage-today"]').text()).toContain('$0.800')
    expect(wrapper.get('[data-testid="period-usage-yesterday"]').text()).toContain('$0.700')
    expect(wrapper.get('[data-testid="period-usage-thisWeek"]').text()).toContain('$1.80')
    expect(wrapper.get('[data-testid="period-usage-lastWeek"]').text()).toContain('$0.200')

    await wrapper.get('[data-testid="quick-filter-thisWeek"]').trigger('click')
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      start_date: '2026-08-03',
      end_date: '2026-08-08',
      granularity: 'day',
      include_stats: false
    }))
    expect(wrapper.get('[data-testid="quick-filter-thisWeek"]').attributes('aria-pressed')).toBe('true')
  })
})
