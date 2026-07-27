<template>
  <section v-if="section === 'scope'" aria-labelledby="prompt-policy-scope-title" class="space-y-6">
    <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
      <div>
        <h3 id="prompt-policy-scope-title" class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.policy.scopeTitle') }}</h3>
        <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.policy.scopeDescription') }}</p>
      </div>
      <div class="inline-flex w-fit rounded-lg bg-gray-100 p-1 dark:bg-dark-700" role="group" :aria-label="t('admin.promptAudit.policy.scope')">
        <button
          type="button"
          class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
          :class="draft.all_groups ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-800 dark:text-white' : 'text-gray-500 dark:text-gray-400'"
          data-test="scope-all-groups"
          @click="patch({ all_groups: true, group_ids: [] })"
        >
          {{ t('admin.promptAudit.policy.allGroups') }}
        </button>
        <button
          type="button"
          class="rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
          :class="!draft.all_groups ? 'bg-white text-gray-900 shadow-sm dark:bg-dark-800 dark:text-white' : 'text-gray-500 dark:text-gray-400'"
          data-test="scope-selected-groups"
          @click="patch({ all_groups: false })"
        >
          {{ t('admin.promptAudit.policy.selectedGroups') }}
        </button>
      </div>
    </div>

    <div v-if="!draft.all_groups" class="space-y-4">
      <div class="relative">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
        <input v-model="groupSearch" type="search" class="input w-full pl-9" :placeholder="t('admin.promptAudit.policy.searchGroups')" :aria-label="t('admin.promptAudit.policy.searchGroups')" />
      </div>
      <div class="grid max-h-[420px] gap-3 overflow-y-auto pr-1 md:grid-cols-2 xl:grid-cols-3">
        <label
          v-for="group in filteredGroups"
          :key="group.id"
          class="flex min-h-20 cursor-pointer items-center justify-between gap-3 rounded-lg border p-4 transition-colors"
          :class="draft.group_ids.includes(group.id) ? 'border-primary-300 bg-primary-50 dark:border-primary-700 dark:bg-primary-900/20' : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'"
        >
          <input class="sr-only" type="checkbox" :checked="draft.group_ids.includes(group.id)" :aria-label="group.name" @change="toggleGroup(group.id)" />
          <span class="min-w-0">
            <span class="block truncate text-sm font-semibold text-gray-900 dark:text-white">{{ group.name }}</span>
            <span class="mt-1 block truncate text-xs text-gray-500 dark:text-gray-400">{{ group.platform }} · {{ group.status }}</span>
          </span>
          <span
            class="flex h-5 w-5 shrink-0 items-center justify-center rounded-full border"
            :class="draft.group_ids.includes(group.id) ? 'border-primary-500 bg-primary-500 text-white' : 'border-gray-300 text-transparent dark:border-dark-500'"
          >
            <Icon name="check" size="xs" :stroke-width="2" />
          </span>
        </label>
        <p v-if="filteredGroups.length === 0" class="py-6 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.policy.noGroups') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
        <span class="rounded-md bg-gray-100 px-2 py-1 dark:bg-dark-700">{{ t('admin.promptAudit.policy.selectedCount', { count: draft.group_ids.length }) }}</span>
        <span v-if="missingGroupIds.length" class="rounded-md bg-amber-50 px-2 py-1 text-amber-700 dark:bg-amber-950/30 dark:text-amber-200">
          {{ t('admin.promptAudit.policy.missingGroups') }}: {{ missingGroupIds.join(', ') }}
        </span>
      </div>
    </div>

    <div class="border-t border-gray-100 pt-6 dark:border-dark-700">
      <div class="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h3 class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.policy.scanners') }}</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.policy.scannersHint') }}</p>
        </div>
        <span class="w-fit rounded-md bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
          {{ t('admin.promptAudit.policy.scannerCount', { count: draft.scanners.length, total: SCANNER_CATALOG.length }) }}
        </span>
      </div>
      <div class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        <label
          v-for="scanner in SCANNER_CATALOG"
          :key="scanner.id"
          class="flex min-h-24 cursor-pointer items-start justify-between gap-3 rounded-lg border p-4 transition-colors"
          :class="draft.scanners.includes(scanner.id) ? 'border-primary-300 bg-primary-50 dark:border-primary-700 dark:bg-primary-900/20' : 'border-gray-100 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700/60'"
        >
          <input class="sr-only" type="checkbox" :checked="draft.scanners.includes(scanner.id)" :aria-label="scannerLabel(scanner.id)" @change="toggleScanner(scanner.id)" />
          <span class="min-w-0">
            <span class="block text-sm font-semibold text-gray-900 dark:text-white">{{ scannerLabel(scanner.id) }}</span>
            <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t(`admin.promptAudit.scannerDescriptions.${scanner.id}`) }}</span>
          </span>
          <span
            class="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full border"
            :class="draft.scanners.includes(scanner.id) ? 'border-primary-500 bg-primary-500 text-white' : 'border-gray-300 text-transparent dark:border-dark-500'"
          >
            <Icon name="check" size="xs" :stroke-width="2" />
          </span>
        </label>
      </div>
    </div>
  </section>

  <section v-else aria-labelledby="prompt-policy-runtime-title" class="space-y-6">
    <div>
      <h3 id="prompt-policy-runtime-title" class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.policy.runtimeTitle') }}</h3>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('admin.promptAudit.policy.runtimeDescription') }}</p>
    </div>

    <div class="grid gap-5 lg:grid-cols-2">
      <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
        <span>{{ t('admin.promptAudit.policy.workerCount') }}</span>
        <input :value="draft.worker_count" type="number" min="1" max="32" class="input w-full" :aria-label="t('admin.promptAudit.policy.workerCount')" @input="patch({ worker_count: Number(($event.target as HTMLInputElement).value) })" />
        <span class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.policy.workerCountHint') }}</span>
      </label>
      <label class="space-y-1.5 text-sm text-gray-700 dark:text-dark-200">
        <span>{{ t('admin.promptAudit.policy.queueCapacity') }}</span>
        <input :value="draft.queue_capacity" type="number" min="1" max="100000" class="input w-full" :aria-label="t('admin.promptAudit.policy.queueCapacity')" @input="patch({ queue_capacity: Number(($event.target as HTMLInputElement).value) })" />
        <span class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.policy.queueCapacityHint') }}</span>
      </label>
    </div>

    <div class="flex items-start gap-3 rounded-lg border border-gray-100 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-900/30">
      <Icon name="server" size="md" class="mt-0.5 shrink-0 text-gray-400" />
      <div>
        <p class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.promptAudit.policy.strategy') }} · priority</p>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.policy.strategyHint') }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PromptAuditDraft, PromptAuditGroup } from '../types'
import { cloneData, SCANNER_CATALOG } from '../viewModel'

const props = withDefaults(defineProps<{
  section?: 'scope' | 'runtime'
  draft: PromptAuditDraft
  groups: PromptAuditGroup[]
}>(), {
  section: 'scope',
})
const emit = defineEmits<{ (event: 'update:draft', value: PromptAuditDraft): void }>()
const { t } = useI18n()
const groupSearch = ref('')

const filteredGroups = computed(() => {
  const query = groupSearch.value.trim().toLowerCase()
  if (!query) return props.groups
  return props.groups.filter((group) => `${group.name} ${group.id} ${group.platform}`.toLowerCase().includes(query))
})
const knownGroupIds = computed(() => new Set(props.groups.map((group) => group.id)))
const missingGroupIds = computed(() => props.draft.group_ids.filter((id) => !knownGroupIds.value.has(id)))

function patch(value: Partial<PromptAuditDraft>) {
  emit('update:draft', { ...cloneData(props.draft), ...value })
}

function toggleGroup(id: number) {
  const selected = new Set(props.draft.group_ids)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  patch({ group_ids: [...selected].sort((a, b) => a - b) })
}

function toggleScanner(id: string) {
  const selected = new Set(props.draft.scanners)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  patch({ scanners: SCANNER_CATALOG.map((item) => item.id).filter((item) => selected.has(item)) })
}

function scannerLabel(id: string): string {
  return t(`admin.promptAudit.scanners.${id}`)
}
</script>
