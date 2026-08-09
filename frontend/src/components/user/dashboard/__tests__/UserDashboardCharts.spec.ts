import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UserDashboardCharts from '../UserDashboardCharts.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('UserDashboardCharts', () => {
  it('offers hour, day, week, and month usage dimensions', () => {
    const wrapper = mount(UserDashboardCharts, {
      props: {
        loading: false,
        startDate: '2026-08-03',
        endDate: '2026-08-09',
        granularity: 'day',
        trend: [],
        models: []
      },
      global: {
        stubs: {
          DateRangePicker: true,
          Select: {
            props: ['options'],
            template: '<div data-testid="granularity-options"><span v-for="option in options" :key="option.value">{{ option.value }}</span></div>'
          },
          LoadingSpinner: true,
          TokenUsageTrend: true,
          Doughnut: true
        }
      }
    })

    expect(wrapper.get('[data-testid="granularity-options"]').text()).toBe('hourdayweekmonth')
  })
})
