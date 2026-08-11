import type { LocationQuery } from 'vue-router'

// Shared query-string coercion for the ops dashboard and the standalone
// detail route: vue-router query values may be string | string[] | null.
export function readRouteQueryString(query: LocationQuery, key: string): string {
  const value = query[key]
  if (typeof value === 'string') return value
  if (Array.isArray(value) && typeof value[0] === 'string') return value[0]
  return ''
}

export function readRouteQueryNumber(query: LocationQuery, key: string): number | null {
  const raw = readRouteQueryString(query, key)
  if (!raw) return null
  const n = Number.parseInt(raw, 10)
  return Number.isFinite(n) ? n : null
}
