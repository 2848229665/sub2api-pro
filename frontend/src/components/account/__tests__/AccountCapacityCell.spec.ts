import type * as VueI18n from 'vue-i18n'
import type { Account } from '@/types'

import { defineComponent } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountCapacityCell from '../AccountCapacityCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof VueI18n>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const CapacityBadgeStub = defineComponent({
  name: 'CapacityBadge',
  props: {
    current: { type: [Number, String], required: true },
    max: { type: [Number, String], required: true },
    suffix: { type: String, default: '' },
    tooltip: { type: String, default: '' },
    colorClass: { type: String, default: '' }
  },
  template: '<div data-testid="capacity-badge" :data-max="max" :data-suffix="suffix" :data-tooltip="tooltip" :data-color-class="colorClass" />'
})

function buildAccount(overrides: Partial<Account>): Account {
  return {
    id: 1,
    name: 'Capacity test',
    platform: 'openai',
    type: 'apikey',
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '',
    updated_at: '',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides
  }
}

function mountCapacity(overrides: Partial<Account>) {
  return mount(AccountCapacityCell, {
    props: { account: buildAccount(overrides) },
    global: {
      stubs: {
        CapacityBadge: CapacityBadgeStub,
        QuotaBadge: true
      }
    }
  })
}

describe('AccountCapacityCell', () => {
  it('shows the configured account concurrency without a per-account reserve split', () => {
    const wrapper = mountCapacity({
      id: 1,
      platform: 'openai',
      type: 'apikey',
      concurrency: 10,
      current_concurrency: 7,
      extra: { affinity_concurrency_reserve: 9 }
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('10')
    expect(badge.attributes('data-suffix')).toBe('')
    expect(badge.attributes('data-tooltip')).toBe('')
  })

  it('keeps non-OpenAI capacity badges unchanged', () => {
    const wrapper = mountCapacity({
      id: 4,
      platform: 'anthropic',
      type: 'oauth',
      concurrency: 5,
      current_concurrency: 0,
      extra: {}
    })

    expect(wrapper.get('[data-testid="capacity-badge"]').attributes('data-suffix')).toBe('')
  })

  it('renders C=0 as unlimited without a full-capacity state', () => {
    const wrapper = mountCapacity({
      id: 5,
      platform: 'openai',
      type: 'apikey',
      concurrency: 0,
      current_concurrency: 12,
      extra: {}
    })

    const badge = wrapper.get('[data-testid="capacity-badge"]')
    expect(badge.attributes('data-max')).toBe('∞')
    expect(badge.attributes('data-suffix')).toBe('')
    expect(badge.attributes('data-tooltip')).toBe('admin.accounts.capacity.concurrency.unlimited')
    expect(badge.attributes('data-color-class')).not.toContain('red')
  })
})
