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
import { Textarea } from '@/components/ui/textarea'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { riskWrite } from '../api'
import { LABELS, type View } from '../constants'
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
  view: View
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
      screening: {
        ...props.defaults.policy.screening,
        ...props.initial.screening,
      },
      model: {
        ...props.defaults.policy.model,
        ...props.initial.model,
        auto_confirm_threshold:
          props.initial.model.auto_confirm_threshold ??
          Math.min(
            props.defaults.policy.model.auto_confirm_threshold,
            props.initial.model.threshold
          ),
      },
      behavior: {
        ...props.defaults.policy.behavior,
        ...props.initial.behavior,
      },
      evidence: {
        ...props.defaults.policy.evidence,
        ...props.initial.evidence,
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
  const r = value.refusal
  const m = value.model
  const b = value.behavior
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
            <FieldGroup className='grid gap-0 overflow-hidden rounded-lg border sm:grid-cols-2 [&>div]:border-b [&>div]:p-3 [&>div]:last:border-b-0 sm:[&>div]:border-b-0 sm:[&>div]:odd:border-r'>
              <Toggle
                disabled={disabled}
                label='Skip GPT models for all layers'
                checked={value.skip_gpt_models}
                onChange={(v) =>
                  form.setValue('skip_gpt_models', v, { shouldDirty: true })
                }
              />
              <Toggle
                disabled={disabled}
                label='Store a 240-character evidence preview'
                checked={value.evidence.store_preview}
                onChange={(v) => update('evidence', { store_preview: v })}
              />
              <Toggle
                disabled={disabled}
                label='Store review evidence'
                checked={value.evidence.store_review_evidence}
                onChange={(store_review_evidence) =>
                  update('evidence', { store_review_evidence })
                }
              />
            </FieldGroup>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Only violation records retain request text. Original role content and submitted audit windows are separate evidence; missing or truncated evidence cannot support a token ban.'
              )}
            </p>
          </FieldSet>
          <Separator />
          <FieldSet className='min-w-0 gap-3'>
            <FieldLegend variant='label'>{t('Model screening')}</FieldLegend>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Keywords and short-term signals accumulate screening points. The decision model runs only when the screening threshold is reached; these points are not confirmed violations.'
              )}
            </p>
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <NumberField
                disabled={disabled}
                label='Screening points required for model review'
                value={value.screening.threshold}
                min={1}
                max={100}
                onChange={(threshold) => update('screening', { threshold })}
              />
              <NumberField
                disabled={disabled}
                label='Screening window (minutes)'
                value={value.screening.window_minutes}
                min={1}
                max={1440}
                onChange={(window_minutes) =>
                  update('screening', { window_minutes })
                }
              />
            </FieldGroup>
          </FieldSet>
          <Separator />
          <FieldSet className='min-w-0 gap-3'>
            <FieldLegend variant='label'>{t('Behavior scoring')}</FieldLegend>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Confirmed explicit content and jailbreak violations add 3 points; other categories add 1. Only enforced requests count. Repeated evidence is deduplicated within the score window.'
              )}
            </p>
            <FieldGroup className='grid gap-4 sm:grid-cols-2'>
              <Toggle
                disabled={disabled}
                label='Enable behavior scoring'
                checked={b.enabled}
                onChange={(enabled) => update('behavior', { enabled })}
              />
              <Toggle
                disabled={disabled}
                label='Automatically suspend the offending API token'
                checked={b.auto_block_enabled}
                onChange={(auto_block_enabled) =>
                  update('behavior', { auto_block_enabled })
                }
              />
              <Toggle
                disabled={disabled}
                label='Automatically confirm high-confidence model violations'
                checked={b.auto_confirm_enabled}
                onChange={(auto_confirm_enabled) =>
                  update('behavior', { auto_confirm_enabled })
                }
              />
            </FieldGroup>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Automatic confirmation adds violation points. Automatic suspension affects only the offending API token, across groups, for the configured duration (30 minutes by default). Permanent bans require an administrator; observation mode never penalizes tokens.'
              )}
            </p>
            <FieldGroup className='grid gap-4 sm:grid-cols-3'>
              <NumberField
                disabled={disabled}
                label='Score window (minutes)'
                value={b.window_minutes}
                min={1}
                max={1440}
                onChange={(window_minutes) =>
                  update('behavior', { window_minutes })
                }
              />
              <NumberField
                disabled={disabled}
                label='Confirmed violation points for suspension'
                value={b.threshold}
                min={1}
                max={100}
                onChange={(threshold) => update('behavior', { threshold })}
              />
              <NumberField
                disabled={disabled}
                label='Block duration (minutes)'
                value={b.block_ttl_minutes}
                min={1}
                max={1440}
                onChange={(block_ttl_minutes) =>
                  update('behavior', { block_ttl_minutes })
                }
              />
            </FieldGroup>
          </FieldSet>
          <Separator />
          {props.view === 'distill' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('When to block')}</FieldLegend>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Ordinary distillation keywords, formatting signals and fingerprints require contextual review by the decision model before blocking.'
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
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('Client response')}
                </FieldLegend>
                <FieldGroup className='grid gap-4 sm:grid-cols-[10rem_1fr]'>
                  <NumberField
                    disabled={disabled}
                    label='Block HTTP status'
                    value={d.error.status}
                    min={400}
                    max={599}
                    onChange={(status) =>
                      update('distill', { error: { ...d.error, status } })
                    }
                  />
                  <Field label='Block message'>
                    <Input
                      disabled={disabled}
                      aria-label={t('Block message')}
                      value={d.error.message}
                      onChange={(e) =>
                        update('distill', {
                          error: { ...d.error, message: e.target.value },
                        })
                      }
                    />
                  </Field>
                </FieldGroup>
              </FieldSet>
            </>
          )}
          {props.view === 'hard_regex' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('When to block')}</FieldLegend>
                <FieldGroup className='grid gap-3 sm:grid-cols-2'>
                  <Toggle
                    disabled={disabled}
                    label='Enable hard regex'
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
                    'Review rules, and any rule hit in system, assistant or tool text, are sent to the decision model, which makes the final call. Without the decision model they are only recorded.'
                  )}
                </p>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Built-in rules keep their default handling. Change the pattern or category to customize handling.'
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
                          options={[
                            'jailbreak',
                            'crack',
                            'nsfw',
                            'distill',
                            'custom',
                          ].map((c) => ({ value: c, label: LABELS[c] ?? c }))}
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
                      id: 'action',
                      header: t('Handling'),
                      cell: (rule, i) => {
                        const builtin = props.defaults.builtin_rules.find(
                          (defaultRule) =>
                            defaultRule.category === rule.category &&
                            defaultRule.source === rule.source.trim()
                        )
                        return (
                          <Choice
                            disabled={disabled || !!builtin}
                            hideLabel
                            label='Handling'
                            value={builtin?.action ?? rule.action ?? 'block'}
                            options={[
                              {
                                value: 'review',
                                label: 'Review by the decision model',
                              },
                              { value: 'block', label: 'Block directly' },
                              {
                                value: 'signal',
                                label: 'Screen current user text only',
                              },
                            ]}
                            onChange={(action) =>
                              update('hard_regex', {
                                rules: rules.map((old, n) =>
                                  n === i
                                    ? {
                                        ...old,
                                        action: action as
                                          | 'block'
                                          | 'review'
                                          | 'signal',
                                      }
                                    : old
                                ),
                              })
                            }
                          />
                        )
                      },
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
                          {
                            category: 'custom',
                            source: '',
                            enabled: true,
                            action: 'review',
                          },
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
                hint='Additional patterns block directly.'
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
          {props.view === 'refusal' && (
            <>
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>{t('When to block')}</FieldLegend>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Similar refusal matches are signals for contextual review by the decision model; similarity alone does not block a request.'
                  )}
                </p>
                <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                  <Toggle
                    disabled={disabled}
                    label='Enable refusal memory'
                    checked={r.enabled}
                    onChange={(enabled) => update('refusal', { enabled })}
                  />
                  <Toggle
                    disabled={disabled}
                    label='Enable similarity matching'
                    checked={r.similarity_enabled}
                    onChange={(similarity_enabled) =>
                      update('refusal', { similarity_enabled })
                    }
                  />
                  <Choice
                    disabled={disabled}
                    label='Similarity threshold'
                    value={String(r.similarity)}
                    options={[80, 85, 90, 95].map((n) => ({
                      value: String(n),
                      label: `${n}%`,
                    }))}
                    onChange={(similarity) =>
                      update('refusal', {
                        similarity: Number(similarity) as 80 | 85 | 90 | 95,
                      })
                    }
                  />
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('Retention and limits')}
                </FieldLegend>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Existing subject blocks remain available below. New API token restrictions use confirmed behavior scores, not hit counts.'
                  )}
                </p>
                <FieldGroup className='grid gap-4 sm:grid-cols-2'>
                  <NumberField
                    disabled={disabled}
                    label='Memory retention (days)'
                    value={r.memory_ttl_days}
                    onChange={(memory_ttl_days) =>
                      update('refusal', { memory_ttl_days })
                    }
                  />
                  <NumberField
                    disabled={disabled}
                    label='Maximum similarity candidates'
                    min={1}
                    max={10000}
                    value={r.max_similar_candidates}
                    onChange={(max_similar_candidates) =>
                      update('refusal', { max_similar_candidates })
                    }
                  />
                </FieldGroup>
              </FieldSet>
            </>
          )}
          {props.view === 'model' && (
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
                      update('model', { provider_id: Number(provider_id) })
                    }
                  />
                  <Field label='Model'>
                    <Input
                      disabled={disabled}
                      aria-label={t('Model')}
                      value={m.model}
                      onChange={(e) =>
                        update('model', { model: e.target.value })
                      }
                    />
                  </Field>
                </FieldGroup>
              </FieldSet>
              <Separator />
              <FieldSet className='min-w-0 gap-3'>
                <FieldLegend variant='label'>
                  {t('Scoring and limits')}
                </FieldLegend>
                <FieldGroup className='grid gap-4 sm:grid-cols-2 lg:grid-cols-3'>
                  <NumberField
                    disabled={disabled}
                    label='Safety threshold'
                    value={m.threshold}
                    step={0.01}
                    max={1}
                    onChange={(threshold) => update('model', { threshold })}
                  />
                  <NumberField
                    disabled={disabled}
                    label='Automatic confirmation safety threshold'
                    value={m.auto_confirm_threshold}
                    min={0}
                    max={m.threshold}
                    step={0.01}
                    onChange={(auto_confirm_threshold) =>
                      update('model', { auto_confirm_threshold })
                    }
                  />
                  <NumberField
                    disabled={disabled}
                    label='Near-miss tolerance'
                    value={m.near_miss_band}
                    step={0.01}
                    max={1}
                    onChange={(near_miss_band) =>
                      update('model', { near_miss_band })
                    }
                  />
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
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Lower model safety scores mean higher risk. Automatic confirmation requires a score below its threshold and complete violation evidence.'
                  )}
                </p>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'The decision model reviews the final characters of the combined system and user text. Trimming to this window is not a model failure. Rule matches outside the window are reviewed separately using bounded excerpts.'
                  )}
                </p>
              </FieldSet>
              <Separator />
              <QuestionBank
                disabled={disabled}
                value={m.questions}
                defaults={props.defaults.builtin_questions}
                onChange={(questions) => update('model', { questions })}
              />
              <Field label='Additional safety question'>
                <Textarea
                  disabled={disabled}
                  aria-label={t('Additional safety question')}
                  value={m.custom_instruction}
                  onChange={(e) =>
                    update('model', { custom_instruction: e.target.value })
                  }
                />
              </Field>
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
                  'Save the group configuration above before saving these settings.'
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
