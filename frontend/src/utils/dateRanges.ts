export interface LocalDateRange {
  start: string
  end: string
}

export type NaturalUsagePeriod = 'today' | 'yesterday' | 'thisWeek' | 'lastWeek'

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
