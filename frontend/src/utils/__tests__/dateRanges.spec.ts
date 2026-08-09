import { describe, expect, it } from 'vitest'

import {
  getUsagePeriodRange,
  getUsageTrendGranularityForRange
} from '../dateRanges'

const rangeForDays = (days: number): { start: string; end: string } => {
  const start = new Date(Date.UTC(2024, 0, 1))
  const end = new Date(start.getTime() + (days - 1) * 24 * 60 * 60 * 1000)
  return {
    start: start.toISOString().slice(0, 10),
    end: end.toISOString().slice(0, 10)
  }
}

describe('getUsageTrendGranularityForRange', () => {
  it.each([
    [1, 'hour'],
    [2, 'hour'],
    [3, 'day'],
    [90, 'day'],
    [91, 'week'],
    [730, 'week'],
    [731, 'month']
  ] as const)('uses %s calendar days as %s buckets', (days, expected) => {
    const range = rangeForDays(days)
    expect(getUsageTrendGranularityForRange(range.start, range.end)).toBe(expected)
  })

  it('falls back to day for an invalid range', () => {
    expect(getUsageTrendGranularityForRange('invalid', '2026-08-09')).toBe('day')
  })
})

describe('getUsagePeriodRange', () => {
  const referenceDate = new Date(2026, 7, 9, 12, 0, 0)

  it('moves backward one day on every offset', () => {
    expect(getUsagePeriodRange('day', 0, referenceDate)).toEqual({ start: '2026-08-09', end: '2026-08-09' })
    expect(getUsagePeriodRange('day', 1, referenceDate)).toEqual({ start: '2026-08-08', end: '2026-08-08' })
    expect(getUsagePeriodRange('day', 2, referenceDate)).toEqual({ start: '2026-08-07', end: '2026-08-07' })
  })

  it('moves backward through Monday-based weeks without a limit', () => {
    expect(getUsagePeriodRange('week', 0, referenceDate)).toEqual({ start: '2026-08-03', end: '2026-08-09' })
    expect(getUsagePeriodRange('week', 1, referenceDate)).toEqual({ start: '2026-07-27', end: '2026-08-02' })
    expect(getUsagePeriodRange('week', 2, referenceDate)).toEqual({ start: '2026-07-20', end: '2026-07-26' })
    expect(getUsagePeriodRange('week', 20, referenceDate)).toEqual({ start: '2026-03-16', end: '2026-03-22' })
  })

  it('moves backward through calendar months without a limit', () => {
    expect(getUsagePeriodRange('month', 0, referenceDate)).toEqual({ start: '2026-08-01', end: '2026-08-09' })
    expect(getUsagePeriodRange('month', 1, referenceDate)).toEqual({ start: '2026-07-01', end: '2026-07-31' })
    expect(getUsagePeriodRange('month', 2, referenceDate)).toEqual({ start: '2026-06-01', end: '2026-06-30' })
    expect(getUsagePeriodRange('month', 20, referenceDate)).toEqual({ start: '2024-12-01', end: '2024-12-31' })
  })
})
