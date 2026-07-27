<template>
  <section class="space-y-6" data-test="safeguard-policy-panel">
    <header>
      <div class="flex items-start gap-3">
        <span class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">
          <Icon name="shield" size="sm" />
        </span>
        <div>
          <h3 class="text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.promptAudit.safeguardPolicy.title') }}</h3>
          <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.safeguardPolicy.description') }}</p>
        </div>
      </div>
    </header>

    <div v-if="hasGroq" class="space-y-5">
      <div class="flex items-start gap-3 border-l-2 border-sky-400 bg-sky-50 px-4 py-3 text-sm text-sky-900 dark:border-sky-600 dark:bg-sky-950/30 dark:text-sky-200">
        <Icon name="lock" size="sm" class="mt-0.5 shrink-0" />
        <div>
          <p class="font-medium">{{ t('admin.promptAudit.safeguardPolicy.fixedEnvelopeTitle') }}</p>
          <p class="mt-1 text-xs leading-5">{{ t('admin.promptAudit.safeguardPolicy.fixedEnvelopeHint') }}</p>
        </div>
      </div>

      <label class="block space-y-2">
        <span class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ t('admin.promptAudit.safeguardPolicy.editorLabel') }}</span>
        <textarea
          :value="policy"
          rows="16"
          class="input min-h-[22rem] w-full resize-y whitespace-pre-wrap font-mono text-sm leading-6"
          :aria-label="t('admin.promptAudit.safeguardPolicy.editorLabel')"
          data-test="safeguard-policy-editor"
          @input="updatePolicy"
        />
      </label>

      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="text-xs">
          <span :class="policyCountClass">{{ t('admin.promptAudit.safeguardPolicy.characterCount', { count: policyLength, max: MAX_SAFEGUARD_POLICY_LENGTH }) }}</span>
          <span class="ml-2 text-gray-500 dark:text-gray-400">{{ lengthGuidance }}</span>
        </div>
        <div class="flex flex-wrap justify-end gap-2">
          <button
            type="button"
            class="btn btn-secondary inline-flex items-center gap-2"
            :disabled="isDefaultPolicy"
            data-test="restore-safeguard-policy"
            @click="restoreDefault"
          >
            <Icon name="refresh" size="sm" />
            {{ t('admin.promptAudit.safeguardPolicy.restoreDefault') }}
          </button>
          <button
            type="button"
            class="btn btn-secondary inline-flex items-center gap-2"
            :disabled="previewing || !policyValid || scanners.length === 0"
            data-test="preview-safeguard-policy"
            @click="$emit('preview')"
          >
            <Icon :name="previewing ? 'refresh' : 'eye'" size="sm" :class="previewing ? 'animate-spin' : ''" />
            {{ previewing ? t('admin.promptAudit.safeguardPolicy.previewing') : t('admin.promptAudit.safeguardPolicy.previewAction') }}
          </button>
        </div>
      </div>

      <p v-if="previewError" role="alert" class="text-sm text-red-600 dark:text-red-300" data-test="safeguard-policy-preview-error">{{ previewError }}</p>

      <section v-if="preview" class="space-y-2 border-t border-gray-100 pt-5 dark:border-dark-700" data-test="safeguard-policy-preview">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.promptAudit.safeguardPolicy.previewTitle') }}</h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.safeguardPolicy.previewMeta', { count: preview.prompt_character_count }) }}</p>
          </div>
          <span class="rounded-md bg-gray-100 px-2 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">
            {{ preview.using_default ? t('admin.promptAudit.safeguardPolicy.defaultBadge') : t('admin.promptAudit.safeguardPolicy.customBadge') }}
          </span>
        </div>
        <pre class="max-h-[28rem] overflow-auto whitespace-pre-wrap break-words rounded-lg border border-gray-200 bg-gray-50 p-4 font-mono text-xs leading-5 text-gray-800 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-200">{{ preview.prompt }}</pre>
      </section>

      <div v-if="hasQwen" class="flex items-start gap-3 border-t border-gray-100 pt-5 text-sm text-gray-600 dark:border-dark-700 dark:text-gray-300" data-test="qwen-builtin-policy-note">
        <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0 text-gray-400" />
        <div>
          <p class="font-medium text-gray-800 dark:text-gray-100">{{ t('admin.promptAudit.safeguardPolicy.qwenTitle') }}</p>
          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.safeguardPolicy.qwenHint') }}</p>
        </div>
      </div>
    </div>

    <div v-else-if="hasQwen" class="flex items-start gap-3 border-l-2 border-gray-300 bg-gray-50 px-4 py-4 text-sm text-gray-700 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-200" data-test="qwen-builtin-policy-only">
      <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0" />
      <div>
        <p class="font-medium">{{ t('admin.promptAudit.safeguardPolicy.qwenTitle') }}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.promptAudit.safeguardPolicy.qwenHint') }}</p>
      </div>
    </div>

    <div v-else class="border border-dashed border-gray-300 px-5 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400" data-test="safeguard-policy-empty">
      {{ t('admin.promptAudit.safeguardPolicy.noEndpoint') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { PromptSafeguardPolicyPreview } from '../types'
import {
  MAX_SAFEGUARD_POLICY_LENGTH,
  MIN_SAFEGUARD_POLICY_LENGTH,
  RECOMMENDED_SAFEGUARD_POLICY_MAX_LENGTH,
  RECOMMENDED_SAFEGUARD_POLICY_MIN_LENGTH,
} from '../viewModel'

const props = defineProps<{
  policy: string
  defaultPolicy: string
  scanners: string[]
  hasGroq: boolean
  hasQwen: boolean
  preview: PromptSafeguardPolicyPreview | null
  previewing: boolean
  previewError: string
}>()
const emit = defineEmits<{
  (event: 'update:policy', value: string): void
  (event: 'preview'): void
}>()
const { t } = useI18n()

const normalizedPolicy = computed(() => props.policy.trim())
const policyLength = computed(() => Array.from(normalizedPolicy.value).length)
const policyValid = computed(() => policyLength.value === 0 || (
  policyLength.value >= MIN_SAFEGUARD_POLICY_LENGTH &&
  policyLength.value <= MAX_SAFEGUARD_POLICY_LENGTH
))
const isDefaultPolicy = computed(() => normalizedPolicy.value === props.defaultPolicy.trim())
const policyCountClass = computed(() => policyValid.value
  ? 'font-medium text-gray-700 dark:text-gray-200'
  : 'font-medium text-red-600 dark:text-red-300')
const lengthGuidance = computed(() => {
  if (policyLength.value === 0) return t('admin.promptAudit.safeguardPolicy.emptyUsesDefault')
  if (!policyValid.value) return t('admin.promptAudit.safeguardPolicy.invalidLength', { min: MIN_SAFEGUARD_POLICY_LENGTH, max: MAX_SAFEGUARD_POLICY_LENGTH })
  if (policyLength.value >= RECOMMENDED_SAFEGUARD_POLICY_MIN_LENGTH && policyLength.value <= RECOMMENDED_SAFEGUARD_POLICY_MAX_LENGTH) {
    return t('admin.promptAudit.safeguardPolicy.recommended')
  }
  return t('admin.promptAudit.safeguardPolicy.recommendedRange', { min: RECOMMENDED_SAFEGUARD_POLICY_MIN_LENGTH, max: RECOMMENDED_SAFEGUARD_POLICY_MAX_LENGTH })
})

function updatePolicy(event: Event) {
  emit('update:policy', (event.target as HTMLTextAreaElement).value)
}

function restoreDefault() {
  emit('update:policy', props.defaultPolicy)
}
</script>
