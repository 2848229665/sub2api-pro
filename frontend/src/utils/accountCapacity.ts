export type AccountCapacityError =
  | 'concurrencyNonNegativeInteger'
  | 'concurrencyMustBePositiveInteger'

export interface AccountCapacityValidation {
  concurrency: number | null
  unlimited: boolean
  concurrencyError: AccountCapacityError | null
  valid: boolean
}

export const isNonNegativeInteger = (value: unknown): value is number =>
  typeof value === 'number' &&
  Number.isFinite(value) &&
  Number.isInteger(value) &&
  value >= 0

export function validateAccountCapacity(
  concurrencyValue: unknown,
  options: { allowUnlimited?: boolean } = {}
): AccountCapacityValidation {
  const concurrency = isNonNegativeInteger(concurrencyValue) ? concurrencyValue : null
  const concurrencyError =
    concurrency === null
      ? 'concurrencyNonNegativeInteger'
      : concurrency === 0 && options.allowUnlimited === false
        ? 'concurrencyMustBePositiveInteger'
        : null

  const valid = concurrencyError === null
  return {
    concurrency,
    unlimited: valid && concurrency === 0,
    concurrencyError,
    valid
  }
}
