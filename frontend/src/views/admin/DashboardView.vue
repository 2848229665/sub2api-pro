<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- Row 1: Core Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Total API Keys -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30">
                <Icon name="key" size="md" class="text-blue-600 dark:text-blue-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.apiKeys') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_api_keys }}
                </p>
                <p class="text-xs text-green-600 dark:text-green-400">
                  {{ stats.active_api_keys }} {{ t('common.active') }}
                </p>
              </div>
            </div>
          </div>

          <!-- Service Accounts -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30">
                <Icon name="server" size="md" class="text-purple-600 dark:text-purple-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.accounts') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_accounts }}
                </p>
                <p class="text-xs">
                  <span class="text-green-600 dark:text-green-400"
                    >{{ stats.normal_accounts }} {{ t('common.active') }}</span
                  >
                  <span v-if="stats.error_accounts > 0" class="ml-1 text-red-500"
                    >{{ stats.error_accounts }} {{ t('common.error') }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Today Requests -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30">
                <Icon name="chart" size="md" class="text-green-600 dark:text-green-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayRequests') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.today_requests }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_requests) }}
                </p>
              </div>
            </div>
          </div>

          <!-- New Users Today -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30">
                <Icon name="userPlus" size="md" class="text-emerald-600 dark:text-emerald-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.users') }}
                </p>
                <p class="text-xl font-bold text-emerald-600 dark:text-emerald-400">
                  +{{ stats.today_new_users }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_users) }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <!-- Row 2: Token Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Today Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30">
                <Icon name="cube" size="md" class="text-amber-600 dark:text-amber-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.today_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.today_actual_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.accountCost')"
                    >${{ formatCost(stats.today_account_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                    >${{ formatCost(stats.today_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Total Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-indigo-100 p-2 dark:bg-indigo-900/30">
                <Icon name="database" size="md" class="text-indigo-600 dark:text-indigo-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.totalTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.total_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-green-600 dark:text-green-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.total_actual_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-orange-500 dark:text-orange-400"
                    :title="t('admin.dashboard.accountCost')"
                    >${{ formatCost(stats.total_account_cost) }}</span
                  >
                  <span class="text-gray-400 dark:text-gray-500"> / </span>
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                    >${{ formatCost(stats.total_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Performance (RPM/TPM) -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30">
                <Icon name="bolt" size="md" class="text-violet-600 dark:text-violet-400" :stroke-width="2" />
              </div>
              <div class="flex-1">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.performance') }}
                </p>
                <div class="flex items-baseline gap-2">
                  <p class="text-xl font-bold text-gray-900 dark:text-white">
                    {{ formatTokens(stats.rpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">RPM</span>
                </div>
                <div class="flex items-baseline gap-2">
                  <p class="text-sm font-semibold text-violet-600 dark:text-violet-400">
                    {{ formatTokens(stats.tpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">TPM</span>
                </div>
              </div>
            </div>
          </div>

          <!-- Avg Response Time -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-rose-100 p-2 dark:bg-rose-900/30">
                <Icon name="clock" size="md" class="text-rose-600 dark:text-rose-400" :stroke-width="2" />
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.avgResponse') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatDuration(stats.average_duration_ms) }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <!-- Natural Period Usage -->
        <div class="card p-4">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t('admin.dashboard.periodUsage') }}
              </h2>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.dashboard.periodUsageHint') }}
              </p>
            </div>
            <div class="flex items-center rounded-lg bg-gray-100 p-1 dark:bg-dark-800">
              <button
                v-for="option in usagePeriodDimensionOptions"
                :key="option.value"
                type="button"
                :data-testid="`period-dimension-${option.value}`"
                :aria-pressed="activeUsageDimension === option.value"
                :class="[
                  'rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                  activeUsageDimension === option.value
                    ? 'bg-white text-primary-600 shadow-sm dark:bg-dark-700 dark:text-primary-400'
                    : 'text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'
                ]"
                @click="selectUsageDimension(option.value)"
              >
                {{ option.label }}
              </button>
            </div>
          </div>
          <div class="flex flex-col gap-3 lg:flex-row lg:items-stretch">
            <button
              type="button"
              data-testid="period-previous"
              class="btn btn-secondary flex min-h-12 items-center justify-center gap-1 lg:w-28"
              @click="navigateUsagePeriod(1)"
            >
              <Icon name="chevronLeft" size="sm" />
              {{ periodNavigationLabels.previous }}
            </button>

            <div class="min-w-0 flex-1 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800/50">
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <p data-testid="period-usage-title" class="text-base font-semibold text-gray-900 dark:text-white">
                    {{ selectedUsagePeriodLabel }}
                  </p>
                  <p data-testid="period-usage-range" class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ formatPeriodRange(periodUsage) }}
                  </p>
                </div>
                <Icon name="chart" size="sm" class="mt-0.5 text-primary-500 dark:text-primary-400" />
              </div>
              <div v-if="periodUsageLoading" class="mt-4 h-12 animate-pulse rounded-lg bg-gray-200 dark:bg-dark-700"></div>
              <div v-else class="mt-4 grid grid-cols-3 gap-3">
                <div>
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.dashboard.spendShort') }}</p>
                  <p data-testid="period-usage-cost" class="mt-1 text-lg font-bold text-emerald-600 dark:text-emerald-400">
                    ${{ formatCost(periodUsage.actualCost) }}
                  </p>
                </div>
                <div>
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.dashboard.requestsShort') }}</p>
                  <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                    {{ formatNumber(periodUsage.requests) }}
                  </p>
                </div>
                <div>
                  <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.dashboard.tokensShort') }}</p>
                  <p class="mt-1 text-lg font-semibold text-gray-900 dark:text-white">
                    {{ formatTokens(periodUsage.tokens) }}
                  </p>
                </div>
              </div>
            </div>

            <div class="grid grid-cols-2 gap-2 lg:w-56 lg:grid-cols-1">
              <button
                type="button"
                data-testid="period-current"
                class="btn btn-secondary min-h-12"
                :disabled="usagePeriodOffset === 0"
                @click="resetUsagePeriod"
              >
                {{ periodNavigationLabels.current }}
              </button>
              <button
                type="button"
                data-testid="period-next"
                class="btn btn-secondary flex min-h-12 items-center justify-center gap-1"
                :disabled="usagePeriodOffset === 0"
                @click="navigateUsagePeriod(-1)"
              >
                {{ periodNavigationLabels.next }}
                <Icon name="chevronRight" size="sm" />
              </button>
            </div>
          </div>
        </div>

        <!-- Quick Actions -->
        <div class="card p-4">
          <div class="mb-3 flex items-center justify-between">
            <h2 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.quickActions') }}
            </h2>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <button
              v-if="canUseBatchImage"
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-sky-50 dark:bg-dark-800/50 dark:hover:bg-sky-900/20"
              @click="router.push('/batch-image')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-sky-100 text-sky-600 dark:bg-sky-900/30 dark:text-sky-400">
                <Icon name="sparkles" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.batchImage') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.batchImageDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-sky-500" />
            </button>
            <button
              type="button"
              class="group flex items-center gap-3 rounded-lg bg-gray-50 p-3 text-left transition-colors hover:bg-emerald-50 dark:bg-dark-800/50 dark:hover:bg-emerald-900/20"
              @click="router.push('/admin/groups')"
            >
              <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400">
                <Icon name="grid" size="md" :stroke-width="2" />
              </span>
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('admin.dashboard.groupPricing') }}
                </span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.groupPricingDesc') }}
                </span>
              </span>
              <Icon name="chevronRight" size="sm" class="text-gray-400 group-hover:text-emerald-500" />
            </button>
          </div>
        </div>

        <!-- Charts Section -->
        <div class="space-y-6">
          <!-- Date Range Filter -->
          <div class="card p-4">
            <div class="space-y-3">
              <div class="flex flex-wrap items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.dashboard.quickRanges') }}:
                </span>
                <button
                  v-for="period in quickPeriodFilters"
                  :key="`filter-${period.key}`"
                  type="button"
                  :data-testid="`quick-filter-${period.key}`"
                  :aria-pressed="activePeriodFilter === period.key"
                  :class="[
                    'rounded-lg border px-3 py-1.5 text-xs font-medium transition-colors',
                    activePeriodFilter === period.key
                      ? 'border-primary-500 bg-primary-600 text-white'
                      : 'border-gray-200 bg-white text-gray-600 hover:border-primary-300 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-primary-700 dark:hover:text-primary-400'
                  ]"
                  @click="selectPeriodFilter(period.key)"
                >
                  {{ period.label }}
                </button>
              </div>
              <div class="flex flex-wrap items-center gap-4">
                <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.timeRange') }}:</span
                >
                <DateRangePicker
                  v-model:start-date="startDate"
                  v-model:end-date="endDate"
                  @change="onDateRangeChange"
                />
                </div>
                <button @click="loadDashboardStats" :disabled="chartsLoading" class="btn btn-secondary">
                  {{ t('common.refresh') }}
                </button>
                <div class="ml-auto flex items-center gap-2">
                  <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                    >{{ t('admin.dashboard.granularity') }}:</span
                  >
                  <div class="w-28">
                    <Select
                      v-model="granularity"
                      :options="granularityOptions"
                      @change="loadChartData"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Charts Grid -->
          <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ModelDistributionChart
              :model-stats="modelStats"
              :enable-ranking-view="true"
              :ranking-items="rankingItems"
              :ranking-total-actual-cost="rankingTotalActualCost"
              :ranking-total-requests="rankingTotalRequests"
              :ranking-total-tokens="rankingTotalTokens"
              :loading="chartsLoading"
              :ranking-loading="rankingLoading"
              :ranking-error="rankingError"
              :start-date="startDate"
              :end-date="endDate"
              @ranking-click="goToUserUsage"
            />
            <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" />
          </div>

          <!-- User Usage Trend (Full Width) -->
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.recentUsage') }} (Top 12)
            </h3>
            <div class="h-64">
              <div v-if="userTrendLoading" class="flex h-full items-center justify-center">
                <LoadingSpinner size="md" />
              </div>
              <Line v-else-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
              <div
                v-else
                class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.dashboard.noDataAvailable') }}
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

const { t, locale } = useI18n()
import { adminAPI } from '@/api/admin'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  UserUsageTrendPoint,
  UserSpendingRankingItem,
  UsageTrendGranularity
} from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import {
  formatLocalDate,
  getNaturalUsagePeriodRanges,
  getUsagePeriodRange,
  getUsageTrendGranularityForRange,
  type LocalDateRange,
  type NaturalUsagePeriod,
  type UsagePeriodDimension
} from '@/utils/dateRanges'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
)

const appStore = useAppStore()
const router = useRouter()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
const periodUsageLoading = ref(false)
const activePeriodFilter = ref<NaturalUsagePeriod | null>(null)
const activeUsageDimension = ref<UsagePeriodDimension>('week')
const usagePeriodOffset = ref(0)
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
let periodUsageLoadSeq = 0
const rankingLimit = 12

interface UsagePeriodSummary extends LocalDateRange {
  requests: number
  tokens: number
  actualCost: number
}

const usagePeriodKeys: NaturalUsagePeriod[] = ['today', 'yesterday', 'thisWeek', 'lastWeek']
const initialUsagePeriodRange = getUsagePeriodRange(activeUsageDimension.value, usagePeriodOffset.value)
const periodUsage = ref<UsagePeriodSummary>({
  ...initialUsagePeriodRange,
  requests: 0,
  tokens: 0,
  actualCost: 0
})

const getLast24HoursRangeDates = (): { start: string; end: string } => {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end)
  }
}

// Date range
const granularity = ref<UsageTrendGranularity>('hour')
const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// Granularity options for Select component
const granularityOptions = computed(() => [
  { value: 'hour', label: t('admin.dashboard.hour') },
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'week', label: t('admin.dashboard.week') },
  { value: 'month', label: t('admin.dashboard.month') }
])

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb'
}))

// Line chart options (for user trend chart)
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    }
  }
}))

// User trend chart data
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by user_id to avoid merging different users with the same display name
  const userGroups = new Map<number, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#14b8a6',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7'
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3
  }))

  return {
    labels: sortedDates,
    datasets
  }
})

// Format helpers
const formatTokens = (value: number | undefined): string => {
  if (value === undefined || value === null) return '0'
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatNumber = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
}

const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

const formatPeriodRange = (range: LocalDateRange): string => {
  const formatDate = (value: string) => {
    const [year, month, day] = value.split('-').map(Number)
    const date = new Date(year, month - 1, day)
    const dateLocale = locale.value.startsWith('zh') ? 'zh-CN' : 'en-US'
    return new Intl.DateTimeFormat(dateLocale, {
      month: 'short',
      day: 'numeric'
    }).format(date)
  }

  if (range.start === range.end) {
    return formatDate(range.start)
  }
  return `${formatDate(range.start)} – ${formatDate(range.end)}`
}

const periodLabelKeys: Record<NaturalUsagePeriod, string> = {
  today: 'dates.today',
  yesterday: 'dates.yesterday',
  thisWeek: 'dates.thisWeek',
  lastWeek: 'dates.lastWeek'
}

const quickPeriodFilters = computed(() =>
  usagePeriodKeys.map((key) => ({
    key,
    label: t(periodLabelKeys[key])
  }))
)

const usagePeriodRange = computed(() =>
  getUsagePeriodRange(activeUsageDimension.value, usagePeriodOffset.value)
)

const usagePeriodDimensionOptions = computed(() => [
  { value: 'day' as const, label: t('admin.dashboard.day') },
  { value: 'week' as const, label: t('admin.dashboard.week') },
  { value: 'month' as const, label: t('admin.dashboard.month') }
])

const previousPeriodLabelKeys: Record<UsagePeriodDimension, string> = {
  day: 'admin.dashboard.previousDay',
  week: 'admin.dashboard.previousWeek',
  month: 'admin.dashboard.previousMonth'
}

const nextPeriodLabelKeys: Record<UsagePeriodDimension, string> = {
  day: 'admin.dashboard.nextDay',
  week: 'admin.dashboard.nextWeek',
  month: 'admin.dashboard.nextMonth'
}

const currentPeriodLabelKeys: Record<UsagePeriodDimension, string> = {
  day: 'dates.today',
  week: 'dates.thisWeek',
  month: 'dates.thisMonth'
}

const periodNavigationLabels = computed(() => ({
  previous: t(previousPeriodLabelKeys[activeUsageDimension.value]),
  current: t(currentPeriodLabelKeys[activeUsageDimension.value]),
  next: t(nextPeriodLabelKeys[activeUsageDimension.value])
}))

const formatPeriodTitleDate = (
  value: string,
  options: Intl.DateTimeFormatOptions
): string => {
  const [year, month, day] = value.split('-').map(Number)
  const dateLocale = locale.value.startsWith('zh') ? 'zh-CN' : 'en-US'
  return new Intl.DateTimeFormat(dateLocale, options).format(new Date(year, month - 1, day))
}

const selectedUsagePeriodLabel = computed(() => {
  const offset = usagePeriodOffset.value
  if (activeUsageDimension.value === 'day') {
    if (offset === 0) return t('dates.today')
    if (offset === 1) return t('dates.yesterday')
    return formatPeriodTitleDate(usagePeriodRange.value.start, {
      month: 'short',
      day: 'numeric',
      weekday: 'short'
    })
  }

  if (activeUsageDimension.value === 'week') {
    if (offset === 0) return t('dates.thisWeek')
    if (offset === 1) return t('dates.lastWeek')
    if (offset === 2) return t('admin.dashboard.twoWeeksAgo')
    return t('admin.dashboard.weeksAgo', { count: offset })
  }

  if (offset === 0) return t('dates.thisMonth')
  if (offset === 1) return t('dates.lastMonth')
  return formatPeriodTitleDate(usagePeriodRange.value.start, {
    year: 'numeric',
    month: 'long'
  })
})

const isNaturalUsagePeriod = (value: string | null): value is NaturalUsagePeriod =>
  value !== null && usagePeriodKeys.includes(value as NaturalUsagePeriod)

const naturalPeriodForUsageNavigator = (): NaturalUsagePeriod | null => {
  if (activeUsageDimension.value === 'day') {
    if (usagePeriodOffset.value === 0) return 'today'
    if (usagePeriodOffset.value === 1) return 'yesterday'
  }
  if (activeUsageDimension.value === 'week') {
    if (usagePeriodOffset.value === 0) return 'thisWeek'
    if (usagePeriodOffset.value === 1) return 'lastWeek'
  }
  return null
}

const syncUsageNavigatorFromPreset = (preset: string | null): boolean => {
  const mappings: Record<string, { dimension: UsagePeriodDimension; offset: number }> = {
    today: { dimension: 'day', offset: 0 },
    yesterday: { dimension: 'day', offset: 1 },
    thisWeek: { dimension: 'week', offset: 0 },
    lastWeek: { dimension: 'week', offset: 1 },
    thisMonth: { dimension: 'month', offset: 0 },
    lastMonth: { dimension: 'month', offset: 1 }
  }
  if (!preset || !mappings[preset]) return false
  activeUsageDimension.value = mappings[preset].dimension
  usagePeriodOffset.value = mappings[preset].offset
  return true
}

const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value
    }
  })
}

const selectPeriodFilter = (period: NaturalUsagePeriod) => {
  const range = getNaturalUsagePeriodRanges()[period]
  syncUsageNavigatorFromPreset(period)
  startDate.value = range.start
  endDate.value = range.end
  granularity.value = getUsageTrendGranularityForRange(range.start, range.end)
  activePeriodFilter.value = period
  void Promise.all([loadChartData(), loadPeriodUsage()])
}

const applyUsagePeriodToDashboard = () => {
  const range = usagePeriodRange.value
  startDate.value = range.start
  endDate.value = range.end
  granularity.value = getUsageTrendGranularityForRange(range.start, range.end)
  activePeriodFilter.value = naturalPeriodForUsageNavigator()
  void Promise.all([loadChartData(), loadPeriodUsage()])
}

const selectUsageDimension = (dimension: UsagePeriodDimension) => {
  activeUsageDimension.value = dimension
  usagePeriodOffset.value = 0
  applyUsagePeriodToDashboard()
}

const navigateUsagePeriod = (direction: 1 | -1) => {
  usagePeriodOffset.value = Math.max(0, usagePeriodOffset.value + direction)
  applyUsagePeriodToDashboard()
}

const resetUsagePeriod = () => {
  if (usagePeriodOffset.value === 0) return
  usagePeriodOffset.value = 0
  applyUsagePeriodToDashboard()
}

// Date range change handler
const onDateRangeChange = (range: {
  startDate: string
  endDate: string
  preset: string | null
}) => {
  activePeriodFilter.value = isNaturalUsagePeriod(range.preset) ? range.preset : null
  granularity.value = getUsageTrendGranularityForRange(range.startDate, range.endDate)
  const syncedPeriod = syncUsageNavigatorFromPreset(range.preset)
  void Promise.all([loadChartData(), ...(syncedPeriod ? [loadPeriodUsage()] : [])])
}

// Load data
const loadDashboardSnapshot = async (includeStats: boolean) => {
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  chartsLoading.value = true
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

const loadUsersTrend = async () => {
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12
    })
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    userTrend.value = []
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

const loadUserSpendingRanking = async () => {
  const currentSeq = ++rankingLoadSeq
  rankingLoading.value = true
  rankingError.value = false
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit
    })
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    rankingItems.value = []
    rankingTotalActualCost.value = 0
    rankingTotalRequests.value = 0
    rankingTotalTokens.value = 0
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const loadPeriodUsage = async () => {
  const currentSeq = ++periodUsageLoadSeq
  const range = { ...usagePeriodRange.value }
  periodUsageLoading.value = true
  periodUsage.value = {
    ...range,
    requests: 0,
    tokens: 0,
    actualCost: 0
  }

  try {
    const response = await adminAPI.dashboard.getUsageTrend({
      start_date: range.start,
      end_date: range.end,
      granularity: getUsageTrendGranularityForRange(range.start, range.end)
    })
    if (currentSeq !== periodUsageLoadSeq) return

    const summary: UsagePeriodSummary = {
      ...range,
      requests: 0,
      tokens: 0,
      actualCost: 0
    }
    for (const point of response.trend || []) {
      summary.requests += toFiniteNumber(point.requests)
      summary.tokens += toFiniteNumber(point.total_tokens)
      summary.actualCost += toFiniteNumber(point.actual_cost)
    }
    periodUsage.value = summary
  } catch (error) {
    if (currentSeq !== periodUsageLoadSeq) return
    console.error('Error loading dashboard period usage:', error)
  } finally {
    if (currentSeq === periodUsageLoadSeq) {
      periodUsageLoading.value = false
    }
  }
}

const loadDashboardStats = async () => {
  await Promise.all([
    loadDashboardSnapshot(true),
    loadUsersTrend(),
    loadUserSpendingRanking(),
    loadPeriodUsage()
  ])
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardSnapshot(false),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

onMounted(() => {
  void refreshBatchImageAccess()
  loadDashboardStats()
})
</script>

<style scoped>
</style>
