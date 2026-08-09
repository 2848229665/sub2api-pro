export interface ModelsListConfig {
  enabled: boolean
  models: string[]
  use_accessible_models: boolean
}

export interface ModelsListItem {
  id: string
  selected: boolean
}

export interface ModelsListState {
  enabled: boolean
  restrictToModelsList: boolean
  savedModels: string[]
  items: ModelsListItem[]
}

export const createModelsListState = (
  config?: Partial<ModelsListConfig> | null,
): ModelsListState => {
  const restrictToModelsList = config?.use_accessible_models ?? false
  return {
    enabled: (config?.enabled ?? false) || restrictToModelsList,
    restrictToModelsList,
    savedModels: normalizeModels(config?.models ?? []),
    items: [],
  }
}

export const hydrateModelsListState = (
  config: Partial<ModelsListConfig> | null | undefined,
  candidates: string[],
): ModelsListState => {
  const state = createModelsListState(config)
  setModelsListCandidates(state, candidates)
  return state
}

export const setModelsListCandidates = (
  state: ModelsListState,
  candidates: string[],
) => {
  const normalizedCandidates = normalizeModels(candidates)
  const currentSelected = new Set(
    state.items.filter(item => item.selected).map(item => item.id),
  )
  const currentKnown = new Set(state.items.map(item => item.id))
  const savedSelected = new Set(state.savedModels)
  const hasExistingItems = state.items.length > 0
  const selectionOrder = normalizeModels([
    ...state.items.map(item => item.id),
    ...state.savedModels,
    ...normalizedCandidates,
  ])

  state.items = selectionOrder.map(id => {
    const selected = hasExistingItems
      ? currentSelected.has(id)
      : state.savedModels.length > 0
        ? savedSelected.has(id)
        : normalizedCandidates.includes(id)

    return {
      id,
      selected: selected && (currentKnown.has(id) || savedSelected.has(id) || state.savedModels.length === 0),
    }
  })
}

export const toggleModelsListItem = (state: ModelsListState, modelID: string) => {
  const item = state.items.find(item => item.id === modelID)
  if (item) {
    item.selected = !item.selected
  }
}

export const selectAllModelsListItems = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = true
  })
}

export const invertModelsListSelection = (state: ModelsListState) => {
  state.items.forEach(item => {
    item.selected = !item.selected
  })
}

export const toggleModelsListAccessRestriction = (state: ModelsListState) => {
  state.restrictToModelsList = !state.restrictToModelsList
  if (state.restrictToModelsList) {
    state.enabled = true
  }
}

export const toggleModelsListEnabled = (state: ModelsListState) => {
  if (state.restrictToModelsList) {
    return
  }
  state.enabled = !state.enabled
}

export const moveModelsListItem = (
  state: ModelsListState,
  fromIndex: number,
  toIndex: number,
) => {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= state.items.length ||
    toIndex >= state.items.length
  ) {
    return
  }
  const [item] = state.items.splice(fromIndex, 1)
  state.items.splice(toIndex, 0, item)
}

export const buildModelsListConfig = (state: ModelsListState): ModelsListConfig => ({
  enabled: state.enabled || state.restrictToModelsList,
  use_accessible_models: state.restrictToModelsList,
  models: state.items.length > 0
    ? state.items.filter(item => item.selected).map(item => item.id)
    : [...state.savedModels],
})

const normalizeModels = (models: string[]): string[] => {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) {
      continue
    }
    seen.add(model)
    out.push(model)
  }
  return out
}
