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
    {{ t('common.notFound') }}
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import OpsErrorDetailsModal from './components/OpsErrorDetailsModal.vue'
import OpsErrorDetailModal from './components/OpsErrorDetailModal.vue'
import OpsRequestDetailsModal, { type OpsRequestDetailsPreset } from './components/OpsRequestDetailsModal.vue'
import { OPS_DETAIL_QUERY, openOpsDetailTab, type OpsDetailKind } from './utils/opsDetailLink'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const qs = (key: string): string => {
  const v = route.query[key]
  if (typeof v === 'string') return v
  if (Array.isArray(v) && typeof v[0] === 'string') return v[0]
  return ''
}
const qn = (key: string): number | null => {
  const raw = qs(key)
  if (!raw) return null
  const n = Number.parseInt(raw, 10)
  return Number.isFinite(n) ? n : null
}

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

const requestPreset = computed<OpsRequestDetailsPreset>(() => {
  const preset: OpsRequestDetailsPreset = {
    title: qs(OPS_DETAIL_QUERY.rdTitle) || t('admin.ops.requestDetails.title')
  }
  const k = qs(OPS_DETAIL_QUERY.rdKind)
  if (k) preset.kind = k as OpsRequestDetailsPreset['kind']
  const sort = qs(OPS_DETAIL_QUERY.rdSort)
  if (sort) preset.sort = sort as OpsRequestDetailsPreset['sort']
  const min = qn(OPS_DETAIL_QUERY.rdMin)
  if (min !== null) preset.min_duration_ms = min
  const max = qn(OPS_DETAIL_QUERY.rdMax)
  if (max !== null) preset.max_duration_ms = max
  return preset
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

onMounted(() => {
  const title = qs(OPS_DETAIL_QUERY.rdTitle)
  if (title) document.title = title
})
</script>
