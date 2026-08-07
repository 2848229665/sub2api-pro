import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ContentModerationKeywordStats } from '@/api/admin/riskControl'
import KeywordStatsView from '@/views/admin/KeywordStatsView.vue'

const { getKeywordHitStats, showError } = vi.hoisted(() => ({
  getKeywordHitStats: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  riskControlAPI: {
    getKeywordHitStats,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError }),
}))

vi.mock('@/utils/apiError', () => ({
  extractApiErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      locale: { value: 'en-US' },
    }),
  }
})

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>',
})

const PaginationStub = defineComponent({
  props: {
    page: { type: Number, default: 1 },
    pageSize: { type: Number, default: 20 },
    total: { type: Number, default: 0 },
  },
  template: '<div class="pagination-stub" />',
})

function populatedStats(): ContentModerationKeywordStats {
  return {
    total_hits: 12,
    user_count: 2,
    keyword_count: 3,
    users: {
      items: [
        {
          user_id: 42,
          username: 'alice',
          user_email: 'alice@example.com',
          hit_count: 7,
          keyword_count: 2,
          last_hit_at: '2026-08-07T08:30:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    },
    keywords: {
      items: [
        {
          keyword: 'secret-token',
          hit_count: 8,
          user_count: 2,
          last_hit_at: '2026-08-07T09:00:00Z',
        },
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1,
    },
  }
}

function emptyStats(): ContentModerationKeywordStats {
  return {
    total_hits: 0,
    user_count: 0,
    keyword_count: 0,
    users: { items: [], total: 0, page: 1, page_size: 20, pages: 0 },
    keywords: { items: [], total: 0, page: 1, page_size: 20, pages: 0 },
  }
}

function mountView() {
  return mount(KeywordStatsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        Icon: true,
        Pagination: PaginationStub,
      },
    },
  })
}

describe('admin KeywordStatsView', () => {
  beforeEach(() => {
    getKeywordHitStats.mockReset()
    showError.mockReset()
    getKeywordHitStats.mockResolvedValue(populatedStats())
  })

  it('loads and renders user and keyword rankings', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getKeywordHitStats).toHaveBeenCalledWith({
      from: undefined,
      to: undefined,
      user_page: 1,
      user_page_size: 20,
      keyword_page: 1,
      keyword_page_size: 20,
    })
    expect(wrapper.get('[data-test="user-ranking"]').text()).toContain('alice')
    expect(wrapper.get('[data-test="user-ranking"]').text()).toContain('alice@example.com')
    expect(wrapper.get('[data-test="user-ranking"]').text()).toContain('7')
    expect(wrapper.get('[data-test="keyword-ranking"]').text()).toContain('secret-token')
    expect(wrapper.get('[data-test="keyword-ranking"]').text()).toContain('8')
  })

  it('passes the selected date range and resets both rankings to page one', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="from-date"]').setValue('2026-08-01')
    await wrapper.get('[data-test="to-date"]').setValue('2026-08-07')
    await wrapper.get('[data-test="apply-filter"]').trigger('click')
    await flushPromises()

    expect(getKeywordHitStats).toHaveBeenLastCalledWith({
      from: '2026-08-01',
      to: '2026-08-07',
      user_page: 1,
      user_page_size: 20,
      keyword_page: 1,
      keyword_page_size: 20,
    })
  })

  it('renders empty states for both rankings', async () => {
    getKeywordHitStats.mockResolvedValueOnce(emptyStats())
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="users-empty"]').text()).toBe('admin.keywordStats.noUserHits')
    expect(wrapper.get('[data-test="keywords-empty"]').text()).toBe('admin.keywordStats.noKeywordHits')
  })

  it('reports loading failures', async () => {
    getKeywordHitStats.mockRejectedValueOnce(new Error('network error'))
    mountView()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.keywordStats.loadFailed')
  })
})
