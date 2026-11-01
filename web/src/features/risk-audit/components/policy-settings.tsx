/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useForm, type PathValue } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { TagInput } from '@/components/tag-input'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FieldGroup, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { riskWrite } from '../api'
import { LABELS, MODERATION_CATEGORIES, type PolicySection } from '../constants'
import { useRiskDraft } from '../hooks/use-risk-draft'
import { policySchema } from '../lib/schema'
import type {
  ConfigView,
  Defaults,
  Policy,
  PolicyRow,
  Provider,
} from '../types'
import { Choice, Field, NumberField, Toggle } from './fields'
import { QuestionBank } from './question-bank'
import { ValidationErrors } from './validation-errors'

export function PolicySettings(props: {
  config: ConfigView
  row: PolicyRow
  initial: Policy
  defaults: Defaults
  providers: Provider[]
  section: PolicySection
  canWrite: boolean
  loadingLatest?: boolean
  activationBlocked?: boolean
  onReload?: () => void
  onSaved: (row: PolicyRow) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const form = useForm<Policy>({
    defaultValues: {
      ...props.initial,
      model: {
        ...props.defaults.policy.model,
        ...props.initial.model,
      },
    },
    resolver: zodResolver(policySchema),
  })
  const value = form.watch()
  useRiskDraft(form.formState.isDirty)
  const update = <K extends keyof Policy>(key: K, patch: Partial<Policy[K]>) =>
    form.setValue(
      key,
      { ...(value[key] as object), ...patch } as PathValue<Policy, K>,
      { shouldDirty: true }
    )
  const save = useMutation({
    meta: { errorToast: false },
    mutationFn: async (request: {
      payload: Policy
      enable: boolean
      config: ConfigView
    }) => {
      const result = await riskWrite<PolicyRow & { config_revision?: number }>(
        'post',
        'policies',
        {
          policy_key: props.row.policy_key,
          name: props.row.name,
          base_revision: props.row.revision,
          payload: request.payload,
          activation: {
            expected_config_revision: request.config.revision,
            ...(request.enable ? { enable: true } : {}),
          },
        }
      )
      if (result.config_revision === undefined) {
        void queryClient.invalidateQueries({ queryKey: ['risk'] })
        throw new Error(
          t(
            'Restart the updated backend to save and enable risk audit settings.'
          )
        )
      }
      return result
    },
    onSuccess: (row, request) => {
      if (row.config_revision !== undefined) {
        queryClient.setQueryData<ConfigView>(['risk', 'config'], {
          revision: row.config_revision,
          payload: {
            ...request.config.payload,
            enabled: request.enable || request.config.payload.enabled,
            default_binding: {
              ...request.config.payload.default_binding,
              policy_key: row.policy_key,
              policy_revision: row.revision,
            },
            group_bindings: request.config.payload.group_bindings.map(
              (binding) => ({
                ...binding,
                policy_key: row.policy_key,
                policy_revision: row.revision,
              })
            ),
          },
        })
      }
      form.reset(request.payload)
      props.onSaved(row)
      toast.success(
        request.enable
          ? t('Settings saved and risk audit enabled.')
          : t('Saved successfully')
      )
      void queryClient.invalidateQueries({ queryKey: ['risk'] })
    },
    onError: (error) => {
      if (getServerErrorStatus(error) !== 409) return
      void queryClient.invalidateQueries({ queryKey: ['risk'] })
    },
  })
  const conflict = getServerErrorStatus(save.error) === 409
  const d = value.distill
  const h = value.hard_regex
  const m = value.model
  const selectedProvider = props.providers.find(
    (provider) => provider.id === m.provider_id
  )
  const openaiModeration = selectedProvider?.provider_type === 'openai'
  const disabled = !props.canWrite || save.isPending || props.loadingLatest
  const bindingsMatch = [
    props.config.payload.default_binding,
    ...props.config.payload.group_bindings,
  ].every(
    (binding) =>
      binding.policy_key === props.row.policy_key &&
      binding.policy_revision === props.row.revision
  )
  const unchanged = !form.formState.isDirty && bindingsMatch
  const saveDisabled =
    disabled ||
    props.activationBlocked ||
    conflict ||
    !props.defaults.policy_save_activation
  const submit = (enable: boolean) =>
    form.handleSubmit((payload) => {
      if (saveDisabled || (unchanged && !enable)) {
        return
      }
      save.mutate({ payload, enable, config: props.config })
    })
  const rules = h.rules?.length ? h.rules : props.defaults.builtin_rules
  return (
    <TitledCard
      title={t('Policy settings')}
      description={t(
        'All audited groups use these settings. Group modes and exemptions are preserved when saving.'
      )}
      className='min-w-0'
      contentClassName='space-y-6'
      disableHoverEffect
    >
      {!props.defaults.policy_save_activation && (
        <Alert>
          <AlertDescription>
            {t(
              'Restart the updated backend to save and enable risk audit settings.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {conflict && (
        <Alert>
          <AlertDescription className='space-y-3'>
            <p>
              {t(
                'The risk audit settings changed. Reload the latest settings before saving. Your draft is kept until you confirm.'
              )}
            </p>
            {props.onReload && (
              <Button
                type='button'
                variant='outline'
                disabled={disabled}
                onClick={props.onReload}
              >
                {t('Reload settings')}
              </Button>
            )}
          </AlertDescription>
        </Alert>
      )}
      <form className='min-w-0' onSubmit={submit(true)}>
        <FieldSet className='min-w-0 gap-6'>
          <FieldSet className='min-w-0 gap-3'>
            <FieldLegend variant='label'>{t('General settings')}</FieldLegend>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Keyword matches only select a request for model review; requests without a match pass and are only counted. Only the decision model can confirm a violation, which rejects the request in enforce mode and deducts the user score.'
              )}
            </p>
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <Toggle
                disabled={disabled}
                label='Skip GPT models for all layers'
                checked={value.skip_gpt_models}
                onChange={(v) =>
                  form.setValue('skip_gpt_models', v, { shouldDirty: true })
                }
              />
            </FieldGroup>
          </FieldSet>
          <Separator />
          {props.section === 'distill' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('When to request model review')}
                </FieldLegend>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Distillation matches, including harvest wrappers, formatting signals and fingerprints, select the request for review by the decision model. Matches alone do not confirm a violation.'
                  )}
                </p>
                <FieldGroup className='grid gap-3 sm:grid-cols-2'>
                  <Toggle
                    disabled={disabled}
                    label='Enable distillation detection'
                    checked={d.enabled}
                    onChange={(enabled) => update('distill', { enabled })}
                  />
                  <Toggle
                    disabled={disabled}
                    label='Skip official clients for ordinary distillation rules'
                    checked={d.skip_official}
                    onChange={(skip_official) =>
                      update('distill', { skip_official })
                    }
                  />
                  <Toggle
                    disabled={disabled}
                    label='Skip GPT models for distillation'
                    checked={d.skip_gpt_models}
                    onChange={(skip_gpt_models) =>
                      update('distill', { skip_gpt_models })
                    }
                  />
                  <Toggle
                    disabled={disabled}
                    label='Require no tools'
                    checked={d.structure.require_no_tools}
                    onChange={(require_no_tools) =>
                      update('distill', {
                        structure: { ...d.structure, require_no_tools },
                      })
                    }
                  />
                  <Toggle
                    disabled={disabled}
                    label='Require a single turn'
                    checked={d.structure.require_single_turn}
                    onChange={(require_single_turn) =>
                      update('distill', {
                        structure: { ...d.structure, require_single_turn },
                      })
                    }
                  />
                </FieldGroup>
                <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                  <NumberField
                    disabled={disabled}
                    label='Minimum output tokens for structural detection'
                    value={d.structure.min_max_tokens}
                    onChange={(min_max_tokens) =>
                      update('distill', {
                        structure: { ...d.structure, min_max_tokens },
                      })
                    }
                  />
                  <Field label='Contest regex'>
                    <Input
                      disabled={disabled}
                      aria-label={t('Contest regex')}
                      value={d.structure.contest_pattern}
                      className='font-mono'
                      onChange={(e) =>
                        update('distill', {
                          structure: {
                            ...d.structure,
                            contest_pattern: e.target.value,
                          },
                        })
                      }
                    />
                  </Field>
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('Rules')}</FieldLegend>
                <Accordion multiple className='rounded-lg border px-3'>
                  {(
                    [
                      'hard_needles',
                      'needles',
                      'patterns',
                      'fingerprints',
                    ] as const
                  ).map((key, index) => (
                    <AccordionItem key={key} value={key}>
                      <AccordionTrigger className='items-center gap-3 py-3 hover:no-underline'>
                        <span className='min-w-0 flex-1'>
                          {t(
                            [
                              'Harvest wrappers',
                              'Ordinary needles',
                              'Additional regex patterns',
                              'Prompt fingerprints',
                            ][index]
                          )}
                        </span>
                        <Badge variant='secondary'>
                          {formatNumber(d[key]?.length ?? 0, locale)}
                        </Badge>
                      </AccordionTrigger>
                      <AccordionContent>
                        <TagInput
                          disabled={disabled}
                          submitOnComma={false}
                          value={d[key] ?? []}
                          onChange={(next) =>
                            update('distill', { [key]: next })
                          }
                          className='max-h-72 overflow-y-auto [&_[data-slot=badge]]:max-w-full [&_[data-slot=badge]]:break-all [&_[data-slot=badge]]:whitespace-normal'
                        />
                      </AccordionContent>
                    </AccordionItem>
                  ))}
                </Accordion>
              </FieldSet>
            </>
          )}
          {props.section === 'keyword' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('When to request model review')}
                </FieldLegend>
                <FieldGroup className='grid gap-3 sm:grid-cols-2'>
                  <Toggle
                    disabled={disabled}
                    label='Enable keyword screening'
                    checked={h.enabled}
                    onChange={(enabled) => update('hard_regex', { enabled })}
                  />
                  <Toggle
                    disabled={disabled}
                    label='Strip system reminders'
                    checked={h.strip_reminders}
                    onChange={(strip_reminders) =>
                      update('hard_regex', { strip_reminders })
                    }
                  />
                  <Toggle
                    disabled={disabled}
                    label='Expand Base64 text'
                    checked={h.expand_base64}
                    onChange={(expand_base64) =>
                      update('hard_regex', { expand_base64 })
                    }
                  />
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('Rules')}</FieldLegend>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Custom expressions use RE2. Distillation rules follow the distillation switch.'
                  )}
                </p>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Every rule match selects the request for model review. Without an available decision model, matches alone never confirm a violation or deduct points.'
                  )}
                </p>
                <StaticDataTable
                  data={rules}
                  className='min-w-0 [&_[data-slot=table-container]]:max-h-96 [&_[data-slot=table-container]]:overflow-auto'
                  tableClassName='min-w-[40rem]'
                  tableProps={{ 'aria-label': t('Rules') }}
                  columns={[
                    {
                      id: 'on',
                      header: t('Enabled'),
                      cell: (rule, i) => (
                        <Toggle
                          compact
                          disabled={disabled}
                          label='Enabled'
                          checked={rule.enabled}
                          onChange={(enabled) =>
                            update('hard_regex', {
                              rules: rules.map((old, n) =>
                                n === i ? { ...old, enabled } : old
                              ),
                            })
                          }
                        />
                      ),
                    },
                    {
                      id: 'category',
                      header: t('Category'),
                      cell: (rule, i) => (
                        <Choice
                          disabled={disabled}
                          hideLabel
                          label='Category'
                          value={rule.category}
                          options={props.defaults.rule_categories.map((c) => ({
                            value: c,
                            label: LABELS[c] ?? c,
                          }))}
                          onChange={(category) =>
                            update('hard_regex', {
                              rules: rules.map((old, n) =>
                                n === i ? { ...old, category } : old
                              ),
                            })
                          }
                        />
                      ),
                    },
                    {
                      id: 'rule',
                      header: t('Regex'),
                      className: 'w-full min-w-64',
                      cell: (rule, i) => (
                        <Input
                          disabled={disabled}
                          className='font-mono'
                          aria-label={t('Regex')}
                          value={rule.source}
                          onChange={(e) =>
                            update('hard_regex', {
                              rules: rules.map((old, n) =>
                                n === i
                                  ? { ...old, source: e.target.value }
                                  : old
                              ),
                            })
                          }
                        />
                      ),
                    },
                    {
                      id: 'delete',
                      header: t('Actions'),
                      cell: (_, i) => (
                        <Button
                          type='button'
                          disabled={disabled}
                          variant='ghost'
                          onClick={() =>
                            update('hard_regex', {
                              rules: rules.filter((_, n) => n !== i),
                            })
                          }
                        >
                          {t('Delete')}
                        </Button>
                      ),
                    },
                  ]}
                />
                <div className='flex flex-wrap gap-2'>
                  <Button
                    type='button'
                    disabled={disabled}
                    variant='outline'
                    onClick={() =>
                      update('hard_regex', {
                        rules: [
                          ...rules,
                          { category: 'custom', source: '', enabled: true },
                        ],
                      })
                    }
                  >
                    {t('Add rule')}
                  </Button>
                  <Button
                    type='button'
                    disabled={disabled}
                    variant='outline'
                    onClick={() => update('hard_regex', { rules: null })}
                  >
                    {t('Restore built-in rules')}
                  </Button>
                </div>
              </FieldSet>
              <Field
                label='Additional regex patterns'
                hint='Additional patterns select the request for model review.'
              >
                <TagInput
                  disabled={disabled}
                  submitOnComma={false}
                  value={h.patterns ?? []}
                  onChange={(patterns) => update('hard_regex', { patterns })}
                  className='max-h-72 overflow-y-auto [&_[data-slot=badge]]:max-w-full [&_[data-slot=badge]]:break-all [&_[data-slot=badge]]:whitespace-normal'
                />
              </Field>
            </>
          )}
          {props.section === 'model' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('When to block')}</FieldLegend>
                <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                  <Toggle
                    disabled={disabled}
                    label='Enable decision model'
                    checked={m.enabled}
                    onChange={(enabled) => update('model', { enabled })}
                  />
                  <Choice
                    disabled={disabled}
                    label='Failure action'
                    value={m.fail_action}
                    options={[
                      { value: 'pass', label: 'Pass' },
                      { value: 'block', label: 'Block' },
                    ]}
                    onChange={(fail_action) =>
                      update('model', {
                        fail_action: fail_action as 'pass' | 'block',
                      })
                    }
                  />
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('Model connection')}
                </FieldLegend>
                <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                  <Choice
                    disabled={disabled}
                    label='Decision provider'
                    value={String(m.provider_id)}
                    options={[
                      { value: '0', label: 'Select a provider' },
                      ...props.providers.map((p) => ({
                        value: String(p.id),
                        label: p.name,
                      })),
                    ]}
                    onChange={(provider_id) =>
                      update('model', {
                        provider_id: Number(provider_id),
                        model: '',
                      })
                    }
                  />
                  <Field label='Model'>
                    <Input
                      disabled={disabled}
                      aria-label={t('Model')}
                      value={m.model}
                      placeholder={
                        selectedProvider?.model ||
                        props.defaults.providers.find(
                          (preset) =>
                            preset.id === selectedProvider?.provider_type
                        )?.default_model ||
                        undefined
                      }
                      onChange={(e) =>
                        update('model', { model: e.target.value })
                      }
                    />
                    {openaiModeration && (
                      <p className='text-muted-foreground text-sm'>
                        {t(
                          'Leave the model blank to use the provider default.'
                        )}
                      </p>
                    )}
                  </Field>
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('Scoring and limits')}
                </FieldLegend>
                <FieldGroup className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                  {!openaiModeration && (
                    <NumberField
                      disabled={disabled}
                      label='Safety threshold'
                      value={m.threshold}
                      step={0.01}
                      max={1}
                      onChange={(threshold) => update('model', { threshold })}
                    />
                  )}
                  <NumberField
                    disabled={disabled}
                    label='Total budget (ms)'
                    min={200}
                    max={8000}
                    value={m.budget_ms}
                    onChange={(budget_ms) => update('model', { budget_ms })}
                  />
                  <NumberField
                    disabled={disabled}
                    label='Maximum attempts'
                    min={1}
                    max={10}
                    value={m.max_attempts}
                    onChange={(max_attempts) =>
                      update('model', { max_attempts })
                    }
                  />
                  <NumberField
                    disabled={disabled}
                    label='Cache duration (seconds)'
                    max={3600}
                    value={m.dedup_sec}
                    onChange={(dedup_sec) => update('model', { dedup_sec })}
                  />
                  <NumberField
                    disabled={disabled}
                    label='Audit window characters'
                    min={256}
                    max={64000}
                    value={m.max_audit_chars}
                    onChange={(max_audit_chars) =>
                      update('model', { max_audit_chars })
                    }
                  />
                </FieldGroup>
                {!openaiModeration && (
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Lower model safety scores mean higher risk. A score below the safety threshold confirms a violation.'
                    )}
                  </p>
                )}
                <p className='text-muted-foreground text-sm'>
                  {openaiModeration
                    ? t(
                        'OpenAI reviews only the latest user text, not system prompts, assistant replies or tool results.'
                      )
                    : t(
                        'The decision model reviews the final characters of the combined system and user text. Trimming to this window is not a model failure. Rule matches outside the window are reviewed separately using bounded excerpts.'
                      )}
                </p>
              </FieldSet>
              <Separator />
              {openaiModeration ? (
                <>
                  <FieldSet className='min-w-0 gap-3'>
                    <FieldLegend variant='label'>
                      {t('Category thresholds')}
                    </FieldLegend>
                    <FieldGroup className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                      {Object.entries(MODERATION_CATEGORIES).map(
                        ([category, label]) => (
                          <NumberField
                            key={category}
                            disabled={disabled}
                            label={label}
                            value={
                              m.moderation_thresholds[category] ?? Number.NaN
                            }
                            step={0.01}
                            max={1}
                            onChange={(threshold) => {
                              const others = Object.entries(
                                m.moderation_thresholds
                              ).filter(([key]) => key !== category)
                              update('model', {
                                moderation_thresholds: Object.fromEntries(
                                  Number.isNaN(threshold)
                                    ? others
                                    : [...others, [category, threshold]]
                                ),
                              })
                            }}
                          />
                        )
                      )}
                    </FieldGroup>
                    <p className='text-muted-foreground text-sm'>
                      {t(
                        'A category confirms a violation when its safety score (1 minus the OpenAI risk score) is below its threshold; an empty category is ignored. Sexual categories count as explicit content, the others as safety.'
                      )}
                    </p>
                    <p className='text-muted-foreground text-sm'>
                      {t(
                        'Transcripts, news and safety policies also raise violence, hate and similar scores without asking for such content, so enable those categories only when your policy forbids the content itself.'
                      )}
                    </p>
                  </FieldSet>
                  <Alert role='status'>
                    <AlertDescription>
                      <p>
                        {t(
                          'OpenAI classifies content in fixed categories, not the intent of a request. Custom questions are preserved but not used by this provider.'
                        )}
                      </p>
                      <p>
                        {t(
                          'Jailbreak, distillation and other intent rules still select requests, but OpenAI checks them only against these categories.'
                        )}
                      </p>
                    </AlertDescription>
                  </Alert>
                </>
              ) : (
                <QuestionBank
                  disabled={disabled}
                  value={m.questions}
                  defaults={props.defaults.builtin_questions}
                  onChange={(questions) => update('model', { questions })}
                />
              )}
            </>
          )}
          <Separator />
          <ValidationErrors error={save.error} />
          {Object.keys(form.formState.errors).length > 0 && (
            <p role='alert' className='text-destructive'>
              {t('Invalid risk audit settings')}
            </p>
          )}
          {props.canWrite && (
            <div className='flex flex-wrap gap-2'>
              <Button
                type='button'
                disabled={saveDisabled}
                onClick={submit(true)}
                className='w-full sm:w-fit'
              >
                {t('Save and enable')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={saveDisabled || unchanged}
                onClick={submit(false)}
                className='w-full sm:w-fit'
              >
                {t('Save settings')}
              </Button>
            </div>
          )}
          {props.activationBlocked && (
            <Alert>
              <AlertDescription>
                {t(
                  'Save the scope and scoring configuration before saving these settings.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Save settings keeps the current audit switch. Save and enable turns risk audit on. Both keep the current observe or enforce modes.'
            )}
          </p>
        </FieldSet>
      </form>
    </TitledCard>
  )
}
