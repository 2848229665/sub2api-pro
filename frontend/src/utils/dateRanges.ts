import type { UsageTrendGranularity } from '@/types'

export interface LocalDateRange {
  start: string
  end: string
}

export type NaturalUsagePeriod = 'today' | 'yesterday' | 'thisWeek' | 'lastWeek'
export type UsagePeriodDimension = 'day' | 'week' | 'month'

const millisecondsPerDay = 24 * 60 * 60 * 1000

const atLocalMidnight = (date: Date): Date =>
  new Date(date.getFullYear(), date.getMonth(), date.getDate())

export const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export const addLocalDays = (date: Date, days: number): Date => {
  const result = atLocalMidnight(date)
  result.setDate(result.getDate() + days)
  return result
}

export const startOfLocalWeek = (date: Date): Date => {
  const normalized = atLocalMidnight(date)
  const daysSinceMonday = (normalized.getDay() + 6) % 7
  return addLocalDays(normalized, -daysSinceMonday)
}

export const getNaturalUsagePeriodRanges = (
  referenceDate = new Date()
): Record<NaturalUsagePeriod, LocalDateRange> => {
  const today = atLocalMidnight(referenceDate)
  const yesterday = addLocalDays(today, -1)
  const thisWeekStart = startOfLocalWeek(today)
  const lastWeekStart = addLocalDays(thisWeekStart, -7)
  const lastWeekEnd = addLocalDays(thisWeekStart, -1)

  return {
    today: {
      start: formatLocalDate(today),
      end: formatLocalDate(today)
    },
    yesterday: {
      start: formatLocalDate(yesterday),
      end: formatLocalDate(yesterday)
    },
    thisWeek: {
      start: formatLocalDate(thisWeekStart),
      end: formatLocalDate(today)
    },
    lastWeek: {
      start: formatLocalDate(lastWeekStart),
      end: formatLocalDate(lastWeekEnd)
    }
  }
}

export const getUsagePeriodRange = (
  dimension: UsagePeriodDimension,
  offset: number,
  referenceDate = new Date()
): LocalDateRange => {
  const today = atLocalMidnight(referenceDate)
  const currentWeekStart = startOfLocalWeek(today)
  const safeOffset = Math.max(0, Math.floor(offset))

  if (dimension === 'day') {
    const date = addLocalDays(today, -safeOffset)
    const value = formatLocalDate(date)
    return { start: value, end: value }
  }

  if (dimension === 'week') {
    const start = addLocalDays(currentWeekStart, -safeOffset * 7)
    const end = safeOffset === 0 ? today : addLocalDays(start, 6)
    return { start: formatLocalDate(start), end: formatLocalDate(end) }
  }

  const start = new Date(today.getFullYear(), today.getMonth() - safeOffset, 1)
  const end = safeOffset === 0
    ? today
    : new Date(start.getFullYear(), start.getMonth() + 1, 0)
  return { start: formatLocalDate(start), end: formatLocalDate(end) }
}

const dateStringToUTC = (value: string): number | null => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value)
  if (!match) return null

  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  return Date.UTC(year, month - 1, day)
}

export const inclusiveCalendarDays = (start: string, end: string): number => {
  const startUTC = dateStringToUTC(start)
  const endUTC = dateStringToUTC(end)
  if (startUTC === null || endUTC === null || endUTC < startUTC) return 0
  return Math.floor((endUTC - startUTC) / millisecondsPerDay) + 1
}

export const getUsageTrendGranularityForRange = (
  start: string,
  end: string
): UsageTrendGranularity => {
  const days = inclusiveCalendarDays(start, end)
  if (days <= 0) return 'day'
  if (days <= 2) return 'hour'
  if (days <= 90) return 'day'
  if (days <= 730) return 'week'
  return 'month'
}
