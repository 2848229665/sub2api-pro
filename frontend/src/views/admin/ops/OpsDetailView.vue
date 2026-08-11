<template>
  <OpsErrorDetailsModal
    v-if="kind === 'error-details'"
    embedded
    :show="true"
    :time-range="timeRange"
    :custom-start-time="customStartTime"
    :custom-end-time="customEndTime"
    :platform="platform"
    :group-id="groupId"
    :error-type="errorType"
    @openErrorDetail="openErrorTab"
  />
  <OpsRequestDetailsModal
    v-else-if="kind === 'request'"
    embedded
    :model-value="true"
    :time-range="timeRange"
    :custom-start-time="customStartTime"
    :custom-end-time="customEndTime"
    :preset="requestPreset"
    :platform="platform"
    :group-id="groupId"
    @openErrorDetail="openErrorTab"
  />
  <OpsErrorDetailModal
    v-else-if="kind === 'error'"
    embedded
    :show="true"
    :error-id="errorId"
    :error-type="errorType"
  />
  <div v-else class="p-6 text-sm text-gray-500 dark:text-dark-400">
    {{ t('admin.ops.detail.invalidLink') }}
  </div>
</template>

<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import type { OpsRequestDetailsKind, OpsRequestDetailsSort } from '@/api/admin/ops'
import OpsErrorDetailsModal from './components/OpsErrorDetailsModal.vue'
import OpsErrorDetailModal from './components/OpsErrorDetailModal.vue'
import OpsRequestDetailsModal, { type OpsRequestDetailsPreset } from './components/OpsRequestDetailsModal.vue'
import { OPS_DETAIL_QUERY, openOpsDetailTab, type OpsDetailKind } from './utils/opsDetailLink'
import { readRouteQueryString, readRouteQueryNumber } from './utils/routeQuery'

const { t, te } = useI18n()
const route = useRoute()
const router = useRouter()

const qs = (key: string): string => readRouteQueryString(route.query, key)
const qn = (key: string): number | null => readRouteQueryNumber(route.query, key)

const kind = computed<OpsDetailKind | ''>(() => {
  const k = qs(OPS_DETAIL_QUERY.kind)
  return k === 'error-details' || k === 'error' || k === 'request' ? k : ''
})

const timeRange = computed(() => qs(OPS_DETAIL_QUERY.timeRange) || '1h')
const platform = computed(() => qs(OPS_DETAIL_QUERY.platform) || '')
const groupId = computed(() => qn(OPS_DETAIL_QUERY.groupId))
const customStartTime = computed(() => qs(OPS_DETAIL_QUERY.customStart) || null)
const customEndTime = computed(() => qs(OPS_DETAIL_QUERY.customEnd) || null)
const errorType = computed<'request' | 'upstream'>(() =>
  qs(OPS_DETAIL_QUERY.errorType) === 'upstream' ? 'upstream' : 'request'
)
const errorId = computed(() => qn(OPS_DETAIL_QUERY.errorId))

// URL params are untrusted: only whitelisted values reach the API, and only
// i18n keys present in the message catalog reach the title.
const allowedRequestKinds = new Set<OpsRequestDetailsKind>(['all', 'success', 'error'])
const allowedRequestSorts = new Set<OpsRequestDetailsSort>(['created_at_desc', 'duration_desc'])

const requestPreset = computed<OpsRequestDetailsPreset>(() => {
  const preset: OpsRequestDetailsPreset = {}
  const titleKey = qs(OPS_DETAIL_QUERY.rdTitleKey)
  if (titleKey && te(titleKey)) preset.titleKey = titleKey
  const k = qs(OPS_DETAIL_QUERY.rdKind)
  if (allowedRequestKinds.has(k as OpsRequestDetailsKind)) preset.kind = k as OpsRequestDetailsKind
  const sort = qs(OPS_DETAIL_QUERY.rdSort)
  if (allowedRequestSorts.has(sort as OpsRequestDetailsSort)) preset.sort = sort as OpsRequestDetailsSort
  const min = qn(OPS_DETAIL_QUERY.rdMin)
  if (min !== null) preset.min_duration_ms = min
  const max = qn(OPS_DETAIL_QUERY.rdMax)
  if (max !== null) preset.max_duration_ms = max
  return preset
})

const pageTitle = computed(() => {
  switch (kind.value) {
    case 'error-details':
      return errorType.value === 'upstream'
        ? t('admin.ops.errorDetails.upstreamErrors')
        : t('admin.ops.errorDetails.requestErrors')
    case 'request':
      return requestPreset.value.titleKey ? t(requestPreset.value.titleKey) : t('admin.ops.requestDetails.title')
    case 'error':
      return t('admin.ops.errorDetail.title')
    default:
      return t('admin.ops.title')
  }
})

watchEffect(() => {
  document.title = pageTitle.value
})

// A single error clicked from an embedded list opens as its own new tab,
// carrying the current time/scope filters forward.
function openErrorTab(id: number) {
  openOpsDetailTab(router, {
    kind: 'error',
    errorId: id,
    errorType: errorType.value,
    timeRange: timeRange.value,
    platform: platform.value,
    groupId: groupId.value,
    customStartTime: customStartTime.value,
    customEndTime: customEndTime.value
  })
}
</script>
