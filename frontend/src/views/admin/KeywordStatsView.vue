<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('admin.keywordStats.title') }}
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.keywordStats.description') }}
          </p>
        </div>
        <button
          type="button"
          class="btn btn-secondary inline-flex items-center gap-2 self-start lg:self-auto"
          :disabled="loading"
          data-test="refresh-stats"
          @click="loadStats"
        >
          <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          {{ t('admin.keywordStats.refresh') }}
        </button>
      </div>

      <div class="card p-4 sm:p-6">
        <div class="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
          <div class="grid flex-1 grid-cols-1 gap-4 sm:grid-cols-2 xl:max-w-2xl">
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.keywordStats.from') }}
              </span>
              <input
                v-model="filters.from"
                type="date"
                class="input w-full"
                :max="filters.to || undefined"
                data-test="from-date"
              />
            </label>
            <label class="block">
              <span class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                {{ t('admin.keywordStats.to') }}
              </span>
              <input
                v-model="filters.to"
                type="date"
                class="input w-full"
                :min="filters.from || undefined"
                data-test="to-date"
              />
            </label>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="btn btn-primary"
              :disabled="loading"
              data-test="apply-filter"
              @click="applyFilters"
            >
              {{ t('admin.keywordStats.apply') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary"
              :disabled="loading || (!filters.from && !filters.to)"
              data-test="clear-filter"
              @click="clearFilters"
            >
              {{ t('admin.keywordStats.clear') }}
            </button>
          </div>
        </div>
        <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.keywordStats.filterHint') }}
        </p>
      </div>

      <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
        <div
          v-for="card in summaryCards"
          :key="card.key"
          class="rounded-xl border border-gray-100 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800"
        >
          <div class="flex items-center justify-between gap-4">
            <div>
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ card.label }}</p>
              <p class="mt-2 text-3xl font-semibold text-gray-900 dark:text-white">
                {{ card.value }}
              </p>
            </div>
            <div class="flex h-11 w-11 items-center justify-center rounded-xl" :class="card.iconClass">
              <Icon :name="card.icon" size="lg" />
            </div>
          </div>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-6 2xl:grid-cols-2">
        <section class="card min-w-0 overflow-hidden" data-test="user-ranking">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.keywordStats.userRanking') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.keywordStats.userRankingHint') }}
            </p>
          </div>

          <div v-if="loading" class="flex min-h-56 items-center justify-center">
            <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
          </div>
          <div v-else-if="stats.users.items.length === 0" class="flex min-h-56 flex-col items-center justify-center px-6 text-center">
            <Icon name="users" size="xl" class="text-gray-300 dark:text-gray-600" />
            <p class="mt-3 text-sm text-gray-500 dark:text-gray-400" data-test="users-empty">
              {{ t('admin.keywordStats.noUserHits') }}
            </p>
          </div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-700/50">
                <tr>
                  <th class="table-header w-16">{{ t('admin.keywordStats.rank') }}</th>
                  <th class="table-header">{{ t('admin.keywordStats.user') }}</th>
                  <th class="table-header text-right">{{ t('admin.keywordStats.hits') }}</th>
                  <th class="table-header text-right">{{ t('admin.keywordStats.distinctKeywords') }}</th>
                  <th class="table-header whitespace-nowrap">{{ t('admin.keywordStats.lastHit') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
                <tr v-for="(item, index) in stats.users.items" :key="userRowKey(item)" data-test="user-row">
                  <td class="table-cell font-semibold text-gray-500 dark:text-gray-400">
                    {{ userRank(index) }}
                  </td>
                  <td class="table-cell min-w-56">
                    <div class="font-medium text-gray-900 dark:text-white">{{ userPrimaryLabel(item) }}</div>
                    <div v-if="userSecondaryLabel(item)" class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                      {{ userSecondaryLabel(item) }}
                    </div>
                    <div class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
                      {{ item.user_id ? `#${item.user_id}` : t('admin.keywordStats.snapshotUser') }}
                    </div>
                  </td>
                  <td class="table-cell text-right text-base font-semibold text-primary-600 dark:text-primary-400">
                    {{ formatNumber(item.hit_count) }}
                  </td>
                  <td class="table-cell text-right font-medium text-gray-700 dark:text-gray-200">
                    {{ formatNumber(item.keyword_count) }}
                  </td>
                  <td class="table-cell whitespace-nowrap text-gray-500 dark:text-gray-400">
                    {{ formatDateTime(item.last_hit_at) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <Pagination
            v-if="stats.users.total > 0"
            :page="userPagination.page"
            :page-size="userPagination.pageSize"
            :total="stats.users.total"
            @update:page="handleUserPageChange"
            @update:pageSize="handleUserPageSizeChange"
          />
        </section>

        <section class="card min-w-0 overflow-hidden" data-test="keyword-ranking">
          <div class="border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('admin.keywordStats.keywordRanking') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.keywordStats.keywordRankingHint') }}
            </p>
          </div>

          <div v-if="loading" class="flex min-h-56 items-center justify-center">
            <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
          </div>
          <div v-else-if="stats.keywords.items.length === 0" class="flex min-h-56 flex-col items-center justify-center px-6 text-center">
            <Icon name="key" size="xl" class="text-gray-300 dark:text-gray-600" />
            <p class="mt-3 text-sm text-gray-500 dark:text-gray-400" data-test="keywords-empty">
              {{ t('admin.keywordStats.noKeywordHits') }}
            </p>
          </div>
          <div v-else class="overflow-x-auto">
            <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
              <thead class="bg-gray-50 dark:bg-dark-700/50">
                <tr>
                  <th class="table-header w-16">{{ t('admin.keywordStats.rank') }}</th>
                  <th class="table-header">{{ t('admin.keywordStats.keyword') }}</th>
                  <th class="table-header text-right">{{ t('admin.keywordStats.hits') }}</th>
                  <th class="table-header text-right">{{ t('admin.keywordStats.hitUsers') }}</th>
                  <th class="table-header whitespace-nowrap">{{ t('admin.keywordStats.lastHit') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-100 bg-white dark:divide-dark-700 dark:bg-dark-800">
                <tr v-for="(item, index) in stats.keywords.items" :key="item.keyword" data-test="keyword-row">
                  <td class="table-cell font-semibold text-gray-500 dark:text-gray-400">
                    {{ keywordRank(index) }}
                  </td>
                  <td class="table-cell min-w-48">
                    <span class="inline-flex max-w-sm break-all rounded-md bg-amber-50 px-2.5 py-1 font-mono text-sm font-medium text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
                      {{ item.keyword }}
                    </span>
                  </td>
                  <td class="table-cell text-right text-base font-semibold text-primary-600 dark:text-primary-400">
                    {{ formatNumber(item.hit_count) }}
                  </td>
                  <td class="table-cell text-right font-medium text-gray-700 dark:text-gray-200">
                    {{ formatNumber(item.user_count) }}
                  </td>
                  <td class="table-cell whitespace-nowrap text-gray-500 dark:text-gray-400">
                    {{ formatDateTime(item.last_hit_at) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <Pagination
            v-if="stats.keywords.total > 0"
            :page="keywordPagination.page"
            :page-size="keywordPagination.pageSize"
            :total="stats.keywords.total"
            @update:page="handleKeywordPageChange"
            @update:pageSize="handleKeywordPageSizeChange"
          />
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  riskControlAPI,
  type ContentModerationKeywordStats,
  type ContentModerationUserHitCount,
} from '@/api/admin'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const appStore = useAppStore()

const filters = reactive({
  from: '',
  to: '',
})
const userPagination = reactive({ page: 1, pageSize: 20 })
const keywordPagination = reactive({ page: 1, pageSize: 20 })
const loading = ref(false)
let requestSequence = 0

const stats = ref<ContentModerationKeywordStats>(emptyStats())

const summaryCards = computed(() => [
  {
    key: 'hits',
    label: t('admin.keywordStats.totalHits'),
    value: formatNumber(stats.value.total_hits),
    icon: 'chart' as const,
    iconClass: 'bg-primary-50 text-primary-600 dark:bg-primary-900/20 dark:text-primary-400',
  },
  {
    key: 'users',
    label: t('admin.keywordStats.hitUsers'),
    value: formatNumber(stats.value.user_count),
    icon: 'users' as const,
    iconClass: 'bg-sky-50 text-sky-600 dark:bg-sky-900/20 dark:text-sky-400',
  },
  {
    key: 'keywords',
    label: t('admin.keywordStats.distinctKeywordTotal'),
    value: formatNumber(stats.value.keyword_count),
    icon: 'key' as const,
    iconClass: 'bg-amber-50 text-amber-600 dark:bg-amber-900/20 dark:text-amber-400',
  },
])

function emptyStats(): ContentModerationKeywordStats {
  return {
    total_hits: 0,
    user_count: 0,
    keyword_count: 0,
    users: { items: [], total: 0, page: 1, page_size: 20, pages: 0 },
    keywords: { items: [], total: 0, page: 1, page_size: 20, pages: 0 },
  }
}

async function loadStats(): Promise<void> {
  const sequence = ++requestSequence
  loading.value = true
  try {
    const result = await riskControlAPI.getKeywordHitStats({
      from: filters.from || undefined,
      to: filters.to || undefined,
      user_page: userPagination.page,
      user_page_size: userPagination.pageSize,
      keyword_page: keywordPagination.page,
      keyword_page_size: keywordPagination.pageSize,
    })
    if (sequence !== requestSequence) return
    stats.value = result
    userPagination.page = result.users.page
    userPagination.pageSize = result.users.page_size
    keywordPagination.page = result.keywords.page
    keywordPagination.pageSize = result.keywords.page_size
  } catch (error: unknown) {
    if (sequence !== requestSequence) return
    appStore.showError(extractApiErrorMessage(error, t('admin.keywordStats.loadFailed')))
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function applyFilters(): void {
  if (filters.from && filters.to && filters.from > filters.to) {
    appStore.showError(t('admin.keywordStats.invalidDateRange'))
    return
  }
  userPagination.page = 1
  keywordPagination.page = 1
  void loadStats()
}

function clearFilters(): void {
  filters.from = ''
  filters.to = ''
  userPagination.page = 1
  keywordPagination.page = 1
  void loadStats()
}

function handleUserPageChange(page: number): void {
  userPagination.page = page
  void loadStats()
}

function handleUserPageSizeChange(pageSize: number): void {
  userPagination.page = 1
  userPagination.pageSize = pageSize
  void loadStats()
}

function handleKeywordPageChange(page: number): void {
  keywordPagination.page = page
  void loadStats()
}

function handleKeywordPageSizeChange(pageSize: number): void {
  keywordPagination.page = 1
  keywordPagination.pageSize = pageSize
  void loadStats()
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat(locale.value).format(value || 0)
}

function formatDateTime(value: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

function userPrimaryLabel(item: ContentModerationUserHitCount): string {
  return item.username || item.user_email || t('admin.keywordStats.unknownUser')
}

function userSecondaryLabel(item: ContentModerationUserHitCount): string {
  return item.username && item.user_email ? item.user_email : ''
}

function userRowKey(item: ContentModerationUserHitCount): string {
  return item.user_id ? `id:${item.user_id}` : `email:${item.user_email}`
}

function userRank(index: number): number {
  return (userPagination.page - 1) * userPagination.pageSize + index + 1
}

function keywordRank(index: number): number {
  return (keywordPagination.page - 1) * keywordPagination.pageSize + index + 1
}

onMounted(() => {
  void loadStats()
})
</script>
