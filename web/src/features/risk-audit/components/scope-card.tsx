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
import { useEffect, useState } from 'react'
import { useForm, type PathValue } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { GroupBadge } from '@/components/group-badge'
import { MultiSelect } from '@/components/multi-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  FieldDescription,
  FieldGroup,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { riskGet, riskWrite } from '../api'
import { LABELS } from '../constants'
import { useRiskDraft } from '../hooks/use-risk-draft'
import { configSchema } from '../lib/schema'
import type { Config, ConfigView, Directory } from '../types'
import { Choice, NumberField, Toggle } from './fields'
import { ValidationErrors } from './validation-errors'

export function ScopeCard(props: {
  config: ConfigView
  directory: Directory
  scoreCategories: string[]
  canWrite: boolean
  onDirtyChange?: (dirty: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<Config>({
    defaultValues: props.config.payload,
    resolver: zodResolver(configSchema),
  })
  const value = form.watch()
  useRiskDraft(form.formState.isDirty)
  const onDirtyChange = props.onDirtyChange
  const [revision, setRevision] = useState(props.config.revision)
  const save = useMutation({
    mutationFn: (payload: Config) =>
      riskWrite<{ revision: number }>('put', 'config', {
        ...payload,
        expected_revision: revision,
      }),
    onSuccess: (result, payload) => {
      queryClient.setQueryData(['risk', 'config'], {
        revision: result.revision,
        payload,
      })
      setRevision(result.revision)
      form.reset(payload)
      toast.success(t('Saved successfully'))
      void queryClient.invalidateQueries({ queryKey: ['risk'] })
    },
  })
  const reload = useMutation({
    mutationFn: () => riskGet<ConfigView>('config'),
    onSuccess: (latest) => {
      queryClient.setQueryData(['risk', 'config'], latest)
      setRevision(latest.revision)
      form.reset(latest.payload)
      save.reset()
      void queryClient.invalidateQueries({ queryKey: ['risk', 'groups'] })
    },
  })
  useEffect(() => {
    onDirtyChange?.(form.formState.isDirty)
  }, [onDirtyChange, form.formState.isDirty])
  useEffect(() => {
    if (!form.formState.isDirty && props.config.revision > revision) {
      form.reset(props.config.payload)
      setRevision(props.config.revision)
    }
  }, [props.config, revision, form, form.formState.isDirty])
  const set = <K extends keyof Config>(key: K, next: Config[K]) =>
    form.setValue(key, next as PathValue<Config, K>, { shouldDirty: true })
  const disabled = !props.canWrite || save.isPending
  const groups = props.directory.groups.filter(
    (g) => g.group_name !== 'auto' && g.namespace !== 'auto'
  )
  const score = value.score
  return (
    <form
      noValidate
      className='min-w-0 space-y-4'
      onSubmit={form.handleSubmit((payload) => {
        if (props.canWrite && !save.isPending) save.mutate(payload)
      })}
    >
      <fieldset className='min-w-0 space-y-4' disabled={disabled}>
        <TitledCard
          title={t('Group exemptions and modes')}
          action={
            <span className='flex flex-wrap items-center gap-2'>
              <Badge variant='secondary'>
                {t(props.config.payload.enabled ? 'Enabled' : 'Disabled')}
              </Badge>
              {form.formState.isDirty && (
                <Badge variant='outline'>{t('Unsaved changes')}</Badge>
              )}
            </span>
          }
          disableHoverEffect
          className='min-w-0'
          contentClassName='space-y-4'
        >
          <Toggle
            disabled={disabled}
            label='Enable risk audit'
            checked={value.enabled}
            onChange={(v) => set('enabled', v)}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              'New groups are audited automatically. Auto requests are always audited, even in exempt groups.'
            )}
          </p>
          <p className='text-muted-foreground text-sm'>
            {t('Existing sensitive-word protection remains independent.')}
          </p>
          <FieldSet className='min-w-0 gap-3'>
            <FieldLegend variant='label'>{t('Default mode')}</FieldLegend>
            <FieldDescription>
              {t(
                'Enforce rejects requests the decision model confirms as violations and deducts the user score. Observe only records violations. Groups inherit the default mode unless overridden below.'
              )}
            </FieldDescription>
            <div className='w-full sm:w-64'>
              <Choice
                label='Mode'
                value={value.default_binding.mode}
                disabled={disabled}
                options={[
                  { value: 'observe', label: 'Observe' },
                  { value: 'enforce', label: 'Enforce' },
                ]}
                onChange={(mode) =>
                  set('default_binding', {
                    ...value.default_binding,
                    mode: mode as Config['default_binding']['mode'],
                  })
                }
              />
            </div>
          </FieldSet>
          <label className='block space-y-2 text-sm' htmlFor='risk-exempt-groups'>
            {t('Exempt groups')}
          </label>
          <MultiSelect
            id='risk-exempt-groups'
            options={groups.map((g) => ({
              value: g.namespace,
              label: g.group_name,
            }))}
            selected={value.exempt_namespaces}
            disabled={disabled}
            onChange={(next) => {
              if (value.group_bindings.some((b) => next.includes(b.namespace))) {
                toast.error(
                  t('Remove the group override before exempting this group.')
                )
                return
              }
              set('exempt_namespaces', next)
            }}
          />
          <FieldDescription>
            {t(
              'Requests from exempt groups skip the risk audit and are only counted. Auto requests are still audited.'
            )}
          </FieldDescription>
          {(props.directory.invalid_refs.length > 0 ||
            props.directory.unmapped.length > 0) && (
            <div role='alert' className='text-warning space-y-2 text-sm'>
              <p>
                {t(
                  'Some group references are inactive. Recreated groups are audited by default.'
                )}
              </p>
              <p>
                {[
                  ...props.directory.invalid_refs.map((r) => r.group_name),
                  ...props.directory.unmapped,
                ].join(', ')}
              </p>
              <Button
                type='button'
                variant='outline'
                onClick={() => {
                  const valid = new Set(groups.map((g) => g.namespace))
                  set(
                    'exempt_namespaces',
                    value.exempt_namespaces.filter((ns) => valid.has(ns))
                  )
                  set(
                    'group_bindings',
                    value.group_bindings.filter((b) => valid.has(b.namespace))
                  )
                }}
              >
                {t('Remove inactive references')}
              </Button>
            </div>
          )}
          <StaticDataTable
            data={groups}
            getRowKey={(g) => g.namespace}
            emptyContent={t('No data')}
            columns={[
              {
                id: 'group',
                header: t('Group'),
                cell: (g) => <GroupBadge group={g.group_name} />,
              },
              {
                id: 'state',
                header: t('Status'),
                cell: (g) =>
                  t(
                    value.exempt_namespaces.includes(g.namespace)
                      ? 'Exempt group'
                      : 'Audited'
                  ),
              },
              {
                id: 'binding',
                header: t('Mode'),
                cell: (g) => {
                  const binding = value.group_bindings.find(
                    (b) => b.namespace === g.namespace
                  )
                  if (value.exempt_namespaces.includes(g.namespace)) {
                    return '—'
                  }
                  return binding ? (
                    <Choice
                      label='Mode'
                      hideLabel
                      value={binding.mode}
                      disabled={disabled}
                      options={[
                        { value: 'observe', label: 'Observe' },
                        { value: 'enforce', label: 'Enforce' },
                      ]}
                      onChange={(mode) =>
                        set(
                          'group_bindings',
                          value.group_bindings.map((b) =>
                            b.namespace === g.namespace
                              ? {
                                  ...b,
                                  mode: mode as Config['default_binding']['mode'],
                                }
                              : b
                          )
                        )
                      }
                    />
                  ) : (
                    <span>
                      {t('Inherited default')} ·{' '}
                      {t(
                        value.default_binding.mode === 'observe'
                          ? 'Observe'
                          : 'Enforce'
                      )}
                    </span>
                  )
                },
              },
              {
                id: 'action',
                header: t('Actions'),
                cell: (g) => {
                  const bound = value.group_bindings.some(
                    (b) => b.namespace === g.namespace
                  )
                  return (
                    <Button
                      type='button'
                      variant='outline'
                      disabled={value.exempt_namespaces.includes(g.namespace)}
                      onClick={() =>
                        set(
                          'group_bindings',
                          bound
                            ? value.group_bindings.filter(
                                (b) => b.namespace !== g.namespace
                              )
                            : [
                                ...value.group_bindings,
                                {
                                  ...value.default_binding,
                                  namespace: g.namespace,
                                },
                              ]
                        )
                      }
                    >
                      {t(bound ? 'Inherit default' : 'Override')}
                    </Button>
                  )
                },
              },
            ]}
          />
        </TitledCard>
        <TitledCard
          title={t('Scoring and bans')}
          description={t(
            'Every user starts with a score of 100. Each confirmed violation deducts the points of its category. A violation while the score is already 0 disables the whole account, or only marks it as ban suggested when automatic banning is off. After 7 days without a violation, the score recovers 5 points per day.'
          )}
          disableHoverEffect
          className='min-w-0'
          contentClassName='space-y-4'
        >
          <FieldGroup className='grid gap-4 sm:grid-cols-2'>
            <Toggle
              disabled={disabled}
              label='Automatically ban accounts at score 0'
              checked={score.auto_ban}
              onChange={(auto_ban) => set('score', { ...score, auto_ban })}
            />
            <Toggle
              disabled={disabled}
              label='Email the user about each violation'
              checked={score.email_notify}
              onChange={(email_notify) =>
                set('score', { ...score, email_notify })
              }
            />
            <NumberField
              disabled={disabled}
              label='Default deduction'
              value={score.default}
              min={1}
              max={100}
              onChange={(points) => set('score', { ...score, default: points })}
            />
            <NumberField
              disabled={disabled}
              label='Violation record retention (days)'
              value={value.retention.violations_days}
              min={1}
              max={3650}
              onChange={(violations_days) =>
                set('retention', { ...value.retention, violations_days })
              }
            />
          </FieldGroup>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Observe mode records violations without rejecting requests or deducting points. Categories without their own value use the default deduction.'
            )}
          </p>
          <StaticDataTable
            data={props.scoreCategories}
            getRowKey={(category) => category}
            tableProps={{ 'aria-label': t('Deduction per category') }}
            columns={[
              {
                id: 'category',
                header: t('Category'),
                cell: (category) => t(LABELS[category] ?? category),
              },
              {
                id: 'points',
                header: t('Deduction'),
                cell: (category) => (
                  <Input
                    type='number'
                    min={1}
                    max={100}
                    step={1}
                    className='w-28'
                    aria-label={t(LABELS[category] ?? category)}
                    disabled={disabled}
                    value={score.deductions[category] ?? score.default}
                    onChange={(e) =>
                      set('score', {
                        ...score,
                        deductions: {
                          ...score.deductions,
                          [category]: e.target.valueAsNumber,
                        },
                      })
                    }
                  />
                ),
              },
              {
                id: 'source',
                header: t('Actions'),
                cell: (category) =>
                  category in score.deductions ? (
                    <Button
                      type='button'
                      variant='ghost'
                      disabled={disabled}
                      onClick={() =>
                        set('score', {
                          ...score,
                          deductions: Object.fromEntries(
                            Object.entries(score.deductions).filter(
                              ([key]) => key !== category
                            )
                          ),
                        })
                      }
                    >
                      {t('Use default')}
                    </Button>
                  ) : (
                    <span className='text-muted-foreground text-sm'>
                      {t('Default deduction')}
                    </span>
                  ),
              },
            ]}
          />
        </TitledCard>
        {getServerErrorStatus(save.error) !== 409 && (
          <ValidationErrors error={save.error} />
        )}
        {Object.keys(form.formState.errors).length > 0 && (
          <p role='alert' className='text-destructive'>
            {t('Invalid risk audit settings')}
          </p>
        )}
        {props.canWrite && (
          <Button
            disabled={save.isPending || !form.formState.isDirty}
            type='submit'
          >
            {t('Save configuration')}
          </Button>
        )}
      </fieldset>
      {getServerErrorStatus(save.error) === 409 && (
        <div role='alert' className='space-y-2'>
          <p>
            {t(
              'The risk audit settings were changed by someone else. Reload and try again.'
            )}
          </p>
          <Button
            type='button'
            onClick={() => reload.mutate()}
            disabled={reload.isPending}
          >
            {t('Reload')}
          </Button>
        </div>
      )}
    </form>
  )
}
