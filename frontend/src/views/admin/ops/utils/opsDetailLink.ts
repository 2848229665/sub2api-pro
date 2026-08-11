import type { Router } from 'vue-router'
import type { OpsRequestDetailsPreset } from '../components/OpsRequestDetailsModal.vue'

// Detail kinds map 1:1 to the dialog bodies that used to open as modals on the
// ops dashboard. Each is now presented in its own browser tab via a shared
// standalone route (`AdminOpsDetail`).
export type OpsDetailKind = 'error-details' | 'error' | 'request'

export interface OpsDetailLinkOptions {
  kind: OpsDetailKind
  timeRange?: string
  platform?: string
  groupId?: number | null
  customStartTime?: string | null
  customEndTime?: string | null
  errorType?: 'request' | 'upstream'
  errorId?: number | null
  requestPreset?: OpsRequestDetailsPreset
}

// Query keys for the detail route. Kept short and stable; the detail view reads
// them back to reconstruct the dialog props.
export const OPS_DETAIL_QUERY = {
  kind: 'kind',
  timeRange: 'tr',
  platform: 'platform',
  groupId: 'group_id',
  customStart: 'cs',
  customEnd: 'ce',
  errorType: 'error_type',
  errorId: 'error_id',
  rdTitle: 'rd_title',
  rdKind: 'rd_kind',
  rdSort: 'rd_sort',
  rdMin: 'rd_min',
  rdMax: 'rd_max'
} as const

export function buildOpsDetailQuery(o: OpsDetailLinkOptions): Record<string, string> {
  const q: Record<string, string> = { [OPS_DETAIL_QUERY.kind]: o.kind }
  if (o.timeRange) q[OPS_DETAIL_QUERY.timeRange] = o.timeRange
  if (o.platform) q[OPS_DETAIL_QUERY.platform] = o.platform
  if (typeof o.groupId === 'number' && o.groupId > 0) q[OPS_DETAIL_QUERY.groupId] = String(o.groupId)
  if (o.customStartTime) q[OPS_DETAIL_QUERY.customStart] = o.customStartTime
  if (o.customEndTime) q[OPS_DETAIL_QUERY.customEnd] = o.customEndTime
  if (o.errorType) q[OPS_DETAIL_QUERY.errorType] = o.errorType
  if (typeof o.errorId === 'number' && o.errorId > 0) q[OPS_DETAIL_QUERY.errorId] = String(o.errorId)
  if (o.requestPreset) {
    const p = o.requestPreset
    if (p.title) q[OPS_DETAIL_QUERY.rdTitle] = p.title
    if (p.kind) q[OPS_DETAIL_QUERY.rdKind] = String(p.kind)
    if (p.sort) q[OPS_DETAIL_QUERY.rdSort] = String(p.sort)
    if (typeof p.min_duration_ms === 'number') q[OPS_DETAIL_QUERY.rdMin] = String(p.min_duration_ms)
    if (typeof p.max_duration_ms === 'number') q[OPS_DETAIL_QUERY.rdMax] = String(p.max_duration_ms)
  }
  return q
}

// Resolve the detail route to an href and open it in a new browser tab.
export function openOpsDetailTab(router: Router, o: OpsDetailLinkOptions): void {
  const href = router.resolve({ name: 'AdminOpsDetail', query: buildOpsDetailQuery(o) }).href
  window.open(href, '_blank', 'noopener')
}
