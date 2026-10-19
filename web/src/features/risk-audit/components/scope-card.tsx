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
import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from '@/components/ui/accordion'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { FieldDescription, FieldLegend, FieldSet } from '@/components/ui/field'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { riskGet, riskWrite } from '../api'
import { useRiskDraft } from '../hooks/use-risk-draft'
import { configSchema } from '../lib/schema'
import type { Config, ConfigView, Directory } from '../types'
import { Choice, Toggle } from './fields'
import { ValidationErrors } from './validation-errors'

export function ScopeCard(props: {
  config: ConfigView
  directory: Directory
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
  const [openSections, setOpenSections] = useState<string[]>(
    props.config.payload.enabled ? [] : ['scope']
  )
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
  const groups = props.directory.groups.filter(
    (g) => g.group_name !== 'auto' && g.namespace !== 'auto'
  )
  return (
    <Accordion
      value={openSections}
      onValueChange={setOpenSections}
      className='bg-card rounded-xl border px-4'
    >
      <AccordionItem value='scope'>
        <AccordionTrigger>
          <span className='flex flex-wrap items-center gap-3'>
            {t('Group exemptions and modes')}
            <Badge variant='secondary'>
              {t(props.config.payload.enabled ? 'Enabled' : 'Disabled')}
            </Badge>
            <Badge
              variant='outline'
              className='max-w-full break-all whitespace-normal'
            >
              {t(
                props.config.payload.default_binding.mode === 'observe'
                  ? 'Observe'
                  : 'Enforce'
              )}
            </Badge>
            {form.formState.isDirty && (
              <Badge variant='outline'>{t('Unsaved changes')}</Badge>
            )}
          </span>
        </AccordionTrigger>
        <AccordionContent keepMounted>
          <form
            className='mt-4 space-y-4'
            onSubmit={form.handleSubmit((payload) => {
              if (props.canWrite && !save.isPending) save.mutate(payload)
            })}
          >
            <fieldset
              className='min-w-0 space-y-4'
              disabled={!props.canWrite || save.isPending}
            >
              <Toggle
                disabled={!props.canWrite || save.isPending}
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
                    'Groups inherit the default mode unless overridden below. Save configuration to apply changes.'
                  )}
                </FieldDescription>
                <div className='w-full sm:w-64'>
                  <Choice
                    label='Mode'
                    value={value.default_binding.mode}
                    disabled={!props.canWrite || save.isPending}
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
              <label
                className='block space-y-2 text-sm'
                htmlFor='risk-exempt-groups'
              >
                {t('Exempt groups')}
              </label>
              <MultiSelect
                id='risk-exempt-groups'
                options={groups.map((g) => ({
                  value: g.namespace,
                  label: g.group_name,
                }))}
                selected={value.exempt_namespaces}
                disabled={!props.canWrite || save.isPending}
                onChange={(next) => {
                  if (
                    value.group_bindings.some((b) => next.includes(b.namespace))
                  ) {
                    toast.error(
                      t(
                        'Remove the group override before exempting this group.'
                      )
                    )
                    return
                  }
                  set('exempt_namespaces', next)
                }}
              />
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
                        value.group_bindings.filter((b) =>
                          valid.has(b.namespace)
                        )
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
                          disabled={!props.canWrite || save.isPending}
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
                          disabled={value.exempt_namespaces.includes(
                            g.namespace
                          )}
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
              {getServerErrorStatus(save.error) !== 409 && (
                <ValidationErrors error={save.error} />
              )}
              {Object.keys(form.formState.errors).length > 0 && (
                <p role='alert'>{t('Invalid risk audit settings')}</p>
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
        </AccordionContent>
      </AccordionItem>
    </Accordion>
  )
}
