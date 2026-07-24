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
import { useEffect, useMemo, useRef } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

const oauthServerSchema = z.object({
  oauth_server: z.object({
    enabled: z.boolean(),
    client_name: z.string().trim().min(1, 'Client name is required'),
    client_id: z.string().trim().min(1, 'Client ID is required'),
    client_secret: z.string(),
    redirect_uris: z.string().trim().min(1, 'Redirect URI is required'),
    allowed_scopes: z.string().trim().min(1, 'Allowed scopes are required'),
    is_public: z.boolean(),
    require_pkce: z.boolean(),
  }),
})

type OAuthServerFormInput = z.input<typeof oauthServerSchema>
type OAuthServerFormValues = z.output<typeof oauthServerSchema>

type FlatOAuthServerDefaults = {
  'oauth_server.enabled': boolean
  'oauth_server.client_name': string
  'oauth_server.client_id': string
  'oauth_server.client_secret': string
  'oauth_server.redirect_uris': string
  'oauth_server.allowed_scopes': string
  'oauth_server.is_public': boolean
  'oauth_server.require_pkce': boolean
}

const splitLines = (value: string) =>
  value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)

const normalizeScopes = (value: string) =>
  value
    .split(/\s+/)
    .map((item) => item.trim())
    .filter(Boolean)
    .join(' ')

const buildFormDefaults = (
  defaults: FlatOAuthServerDefaults
): OAuthServerFormInput => ({
  oauth_server: {
    enabled: defaults['oauth_server.enabled'],
    client_name: defaults['oauth_server.client_name'] ?? '',
    client_id: defaults['oauth_server.client_id'] ?? '',
    client_secret: '',
    redirect_uris: splitLines(
      defaults['oauth_server.redirect_uris'] ?? ''
    ).join('\n'),
    allowed_scopes: normalizeScopes(
      defaults['oauth_server.allowed_scopes'] ?? ''
    ),
    is_public: defaults['oauth_server.is_public'],
    require_pkce: defaults['oauth_server.require_pkce'],
  },
})

const normalizeFormValues = (
  values: OAuthServerFormValues
): FlatOAuthServerDefaults => ({
  'oauth_server.enabled': values.oauth_server.enabled,
  'oauth_server.client_name': values.oauth_server.client_name.trim(),
  'oauth_server.client_id': values.oauth_server.client_id.trim(),
  'oauth_server.client_secret': values.oauth_server.client_secret.trim(),
  'oauth_server.redirect_uris': splitLines(
    values.oauth_server.redirect_uris
  ).join('\n'),
  'oauth_server.allowed_scopes': normalizeScopes(
    values.oauth_server.allowed_scopes
  ),
  'oauth_server.is_public': values.oauth_server.is_public,
  'oauth_server.require_pkce': values.oauth_server.require_pkce,
})

type OAuthServerSectionProps = {
  defaultValues: FlatOAuthServerDefaults
}

export function OAuthServerSection(props: OAuthServerSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const formDefaults = useMemo(
    () => buildFormDefaults(props.defaultValues),
    [props.defaultValues]
  )

  const form = useForm<OAuthServerFormInput, unknown, OAuthServerFormValues>({
    resolver: zodResolver(oauthServerSchema),
    defaultValues: formDefaults,
  })

  const baselineRef = useRef<FlatOAuthServerDefaults>(props.defaultValues)
  const baselineSerializedRef = useRef<string>(
    JSON.stringify(props.defaultValues)
  )

  useEffect(() => {
    const serialized = JSON.stringify(props.defaultValues)
    if (serialized === baselineSerializedRef.current) return
    baselineRef.current = props.defaultValues
    baselineSerializedRef.current = serialized
    form.reset(buildFormDefaults(props.defaultValues))
  }, [props.defaultValues, form])

  const onSubmit = async (values: OAuthServerFormValues) => {
    const normalized = normalizeFormValues(values)
    const comparable = {
      ...normalized,
      'oauth_server.client_secret': '',
    }
    const baselineComparable = {
      ...baselineRef.current,
      'oauth_server.client_secret': '',
    }

    const changedKeys = (
      Object.keys(comparable) as Array<keyof FlatOAuthServerDefaults>
    ).filter((key) => comparable[key] !== baselineComparable[key])

    if (normalized['oauth_server.client_secret'] !== '') {
      changedKeys.push('oauth_server.client_secret')
    }

    if (changedKeys.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const key of changedKeys) {
      await updateOption.mutateAsync({
        key,
        value: normalized[key],
      })
    }

    const nextBaseline = {
      ...normalized,
      'oauth_server.client_secret': '',
    }
    baselineRef.current = nextBaseline
    baselineSerializedRef.current = JSON.stringify(nextBaseline)
    form.reset(buildFormDefaults(nextBaseline))
  }

  return (
    <SettingsSection title={t('OAuth Authorization Server')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
          />

          <FormField
            control={form.control}
            name='oauth_server.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>
                    {t('Enable OAuth authorization server')}
                  </FormLabel>
                  <FormDescription>
                    {t(
                      'Allow third-party clients to sign in through this site'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.client_name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Client Name')}</FormLabel>
                <FormControl>
                  <Input
                    placeholder='Cherry Studio Public Client'
                    autoComplete='off'
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.client_id'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Client ID')}</FormLabel>
                <FormControl>
                  <Input
                    autoComplete='off'
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.client_secret'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Client Secret')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    placeholder={t('Leave blank unless rotating the secret')}
                    autoComplete='new-password'
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Required for confidential clients. Public PKCE clients can leave it blank.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.redirect_uris'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Redirect URIs')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={4}
                    placeholder='cherrystudio://oauth/callback'
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t('One redirect URI per line')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.allowed_scopes'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Allowed Scopes')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={3}
                    placeholder='openid profile email offline_access'
                    value={field.value ?? ''}
                    onChange={(event) => field.onChange(event.target.value)}
                    name={field.name}
                    onBlur={field.onBlur}
                    ref={field.ref}
                  />
                </FormControl>
                <FormDescription>
                  {t('Space-separated OAuth scopes')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.is_public'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Public Client')}</FormLabel>
                  <FormDescription>
                    {t('Disable this for clients that use a client secret')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <FormField
            control={form.control}
            name='oauth_server.require_pkce'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Require PKCE')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Require S256 code challenge for authorization code flow'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
