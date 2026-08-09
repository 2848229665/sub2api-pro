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
        Select: {
          props: ['options'],
          template: '<div data-testid="granularity-options"><span v-for="option in options" :key="option.value">{{ option.value }}</span></div>'
        },
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

  it('offers hour, day, week, and month usage dimensions', async () => {
    const wrapper = mountDashboard()

    await flushPromises()

    expect(wrapper.get('[data-testid="granularity-options"]').text()).toBe('hourdayweekmonth')
  })

  it('continuously navigates previous weeks and months while updating the chart filter', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 7, 9, 12, 0, 0))
    getUsageTrend.mockImplementation(async ({ start_date, end_date, granularity }) => {
      const costs: Record<string, number> = {
        '2026-08-03': 0.9,
        '2026-07-27': 0.8,
        '2026-07-20': 0.7,
        '2026-08-01': 8,
        '2026-07-01': 7,
        '2026-06-01': 6
      }
      return {
        trend: [{
          date: start_date,
          requests: 10,
          total_tokens: 100,
          actual_cost: costs[start_date] ?? 0
        }],
        start_date,
        end_date,
        granularity
      }
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(getUsageTrend).toHaveBeenCalledWith({
      start_date: '2026-08-03',
      end_date: '2026-08-09',
      granularity: 'day'
    })
    expect(wrapper.get('[data-testid="period-usage-title"]').text()).toBe('dates.thisWeek')
    expect(wrapper.get('[data-testid="period-usage-cost"]').text()).toContain('$0.900')

    await wrapper.get('[data-testid="period-previous"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="period-usage-title"]').text()).toBe('dates.lastWeek')
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      start_date: '2026-07-27',
      end_date: '2026-08-02',
      granularity: 'day',
      include_stats: false
    }))

    await wrapper.get('[data-testid="period-previous"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="period-usage-title"]').text()).toBe('admin.dashboard.twoWeeksAgo')
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      start_date: '2026-07-20',
      end_date: '2026-07-26',
      granularity: 'day',
      include_stats: false
    }))

    await wrapper.get('[data-testid="period-dimension-month"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="period-usage-title"]').text()).toBe('dates.thisMonth')

    await wrapper.get('[data-testid="period-previous"]').trigger('click')
    await wrapper.get('[data-testid="period-previous"]').trigger('click')
    await flushPromises()
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      start_date: '2026-06-01',
      end_date: '2026-06-30',
      granularity: 'day',
      include_stats: false
    }))

    await wrapper.get('[data-testid="period-next"]').trigger('click')
    await flushPromises()
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      start_date: '2026-07-01',
      end_date: '2026-07-31'
    }))
  })
})
