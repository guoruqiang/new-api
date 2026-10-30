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
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { TitledCard } from '@/components/ui/titled-card'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { riskWrite } from '../api'
import { LABELS } from '../constants'
import { useRiskDraft } from '../hooks/use-risk-draft'
import type { Defaults, Provider, ProviderInput } from '../types'
import { Choice, Field, Toggle } from './fields'
import { ValidationErrors } from './validation-errors'

const providerSchema = z.object({
  name: z.string().min(1).max(64),
  provider_type: z.enum(['jev', 'laya', 'modernbert', 'openai']),
  base_url: z
    .url()
    .max(300)
    .refine((url) => /^https?:\/\//.test(url)),
  model: z.string().max(120),
  proxy: z.string().max(255),
})

export function ProviderCard(props: {
  provider?: Provider
  presets: Defaults['providers']
  canWrite: boolean
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [replaceKeys, setReplaceKeys] = useState(false)
  const [keys, setKeys] = useState('')
  const [models, setModels] = useState<string[]>([])
  const initial = props.provider ?? {
    name: '',
    provider_type: 'jev',
    base_url: 'https://api.typesafe.ai',
    model: 'jev-latest',
    proxy: '',
  }
  const form = useForm({
    defaultValues: {
      name: initial.name,
      provider_type: initial.provider_type as
        | 'jev'
        | 'laya'
        | 'modernbert'
        | 'openai',
      base_url: initial.base_url,
      model: initial.model,
      proxy: initial.proxy,
    },
    resolver: zodResolver(providerSchema),
  })
  const value = form.watch()
  const explicitKey = replaceKeys
    ? (keys
        .split('\n')
        .map((key) => key.trim())
        .find(Boolean) ?? '')
    : ''
  const connectionChanged =
    value.base_url.trim() !== form.formState.defaultValues?.base_url?.trim() ||
    value.proxy.trim() !== form.formState.defaultValues?.proxy?.trim()
  const saveBeforeProbe =
    !!props.provider?.api_key_count && connectionChanged && !explicitKey
  useRiskDraft(form.formState.isDirty || replaceKeys || !!keys)
  const save = useMutation({
    mutationFn: (input: ProviderInput) =>
      riskWrite<Provider>(
        props.provider ? 'put' : 'post',
        props.provider ? `providers/${props.provider.id}` : 'providers',
        input
      ),
    meta: { errorToast: false },
    onSuccess: (result) => {
      setKeys('')
      setReplaceKeys(false)
      setModels([])
      probe.reset()
      form.reset({
        ...(props.provider ? result : initial),
        provider_type: (props.provider
          ? result.provider_type
          : initial.provider_type) as 'jev' | 'laya' | 'modernbert' | 'openai',
      })
      void queryClient.invalidateQueries({ queryKey: ['risk', 'providers'] })
      toast.success(t('Saved successfully'))
      props.onSaved()
    },
  })
  const probe = useMutation({
    mutationFn: () =>
      riskWrite<{ models: string[] }>('post', 'providers/models', {
        provider_id: props.provider?.id,
        base_url: value.base_url,
        proxy: value.proxy,
        ...(explicitKey ? { api_key: explicitKey } : {}),
      }),
    meta: { errorToast: false },
    onMutate: () => setModels([]),
    onSuccess: (result) => setModels(result.models),
  })
  const disabled = !props.canWrite || save.isPending || probe.isPending
  return (
    <TitledCard
      title={props.provider?.name ?? t('New decision provider')}
      disableHoverEffect
    >
      <form
        className='space-y-5'
        onSubmit={form.handleSubmit((input) =>
          save.mutate({
            ...input,
            ...(replaceKeys
              ? {
                  api_keys: keys
                    .split('\n')
                    .map((k) => k.trim())
                    .filter(Boolean),
                }
              : {}),
          })
        )}
      >
        <fieldset disabled={disabled} className='min-w-0 space-y-4'>
          <div className='grid gap-4 sm:grid-cols-2'>
            <Field label='Name'>
              <Input aria-label={t('Name')} {...form.register('name')} />
            </Field>
            <Choice
              label='Provider type'
              value={value.provider_type}
              options={props.presets.map((p) => ({
                value: p.id,
                label: p.label,
              }))}
              onChange={(type) => {
                const preset = props.presets.find((p) => p.id === type)
                setModels([])
                probe.reset()
                form.setValue(
                  'provider_type',
                  type as 'jev' | 'laya' | 'modernbert' | 'openai',
                  { shouldDirty: true }
                )
                if (preset) {
                  form.setValue('base_url', preset.default_base_url, {
                    shouldDirty: true,
                  })
                  form.setValue('model', preset.default_model, {
                    shouldDirty: true,
                  })
                }
              }}
            />
            <Field label='Base URL'>
              <Input
                aria-label={t('Base URL')}
                {...form.register('base_url')}
              />
            </Field>
            <Field label='Proxy'>
              <Input aria-label={t('Proxy')} {...form.register('proxy')} />
            </Field>
          </div>
          <Field label='Model'>
            <div className='flex flex-col gap-2 sm:flex-row'>
              <Input aria-label={t('Model')} {...form.register('model')} />
              <Button
                type='button'
                variant='outline'
                className='shrink-0'
                disabled={disabled || saveBeforeProbe}
                onClick={() => probe.mutate()}
              >
                {t('Fetch models')}
              </Button>
            </div>
          </Field>
          {value.provider_type === 'openai' && (
            <Alert role='status'>
              <AlertDescription>
                {t(
                  'OpenAI Moderation uses the /v1/moderations endpoint and defaults to omni-moderation-latest. Use an OpenAI API key or a compatible moderation service.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {saveBeforeProbe && (
            <Alert role='status'>
              <AlertDescription>
                {t(
                  'Save the provider before fetching models with stored keys after changing the endpoint or proxy.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {probe.error && (
            <Alert variant='destructive'>
              <AlertDescription>
                {getServerErrorMessage(probe.error)}
              </AlertDescription>
            </Alert>
          )}
          {probe.isSuccess && models.length === 0 && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t('No models were returned by this provider.')}
            </p>
          )}
          {models.length > 0 && (
            <Choice
              label='Available models'
              value={value.model}
              options={[...new Set([value.model, ...models])].map((model) => ({
                value: model,
                label: model,
              }))}
              onChange={(model) =>
                form.setValue('model', model, { shouldDirty: true })
              }
            />
          )}
          <FieldSet className='min-w-0 border-t pt-4'>
            <FieldLegend>{t('Credentials')}</FieldLegend>
            {props.provider && (
              <p className='text-muted-foreground text-sm'>
                {t('Stored keys: {{count}}', {
                  count: props.provider.api_key_count,
                })}{' '}
                ·{' '}
                {props.provider.key_states
                  .map((s, i) => `${i + 1}: ${t(LABELS[s.state] ?? s.state)}`)
                  .join(', ')}
              </p>
            )}
            <Toggle
              disabled={disabled}
              label='Replace stored keys'
              checked={replaceKeys}
              onChange={(v) => {
                setReplaceKeys(v)
                if (!v) setKeys('')
              }}
            />
            {replaceKeys && (
              <Field
                label='API keys (one per line)'
                hint='Keys are write-only. Leaving this replacement empty clears all keys.'
              >
                <Textarea
                  aria-label={t('API keys (one per line)')}
                  autoComplete='off'
                  spellCheck={false}
                  value={keys}
                  onChange={(e) => setKeys(e.target.value)}
                />
              </Field>
            )}
          </FieldSet>
          <div className='flex flex-wrap justify-end gap-2 border-t pt-4'>
            {props.canWrite && (
              <Button type='submit' disabled={disabled}>
                {t('Save provider')}
              </Button>
            )}
          </div>
          <ValidationErrors error={save.error} />
          {Object.keys(form.formState.errors).length > 0 && (
            <p role='alert'>{t('Invalid risk audit settings')}</p>
          )}
        </fieldset>
      </form>
    </TitledCard>
  )
}
