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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import {
  Accordion,
  AccordionItem,
  AccordionTrigger,
  AccordionContent,
} from '@/components/ui/accordion'
import { Button } from '@/components/ui/button'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { riskGet } from './api'
import { Choice } from './components/fields'
import { MemoryRecords } from './components/memory-records'
import { RiskOverview } from './components/overview'
import { PolicySettings } from './components/policy-settings'
import { PreviewDialog } from './components/preview-dialog'
import { ProviderCard } from './components/provider-card'
import { ReviewRecords } from './components/review-records'
import { ScopeCard } from './components/scope-card'
import { ValidationErrors } from './components/validation-errors'
import { DraftGuard } from './hooks/draft-guard'
import { useConfirmRiskDiscard } from './hooks/use-risk-draft'
import type {
  ConfigView,
  Defaults,
  Directory,
  Policy,
  PolicyRow,
  Provider,
} from './types'

export function RiskAudit() {
  return (
    <DraftGuard>
      <RiskAuditContent />
    </DraftGuard>
  )
}

function RiskAuditContent() {
  const client = useQueryClient()
  const defaults = useQuery({
    queryKey: ['risk', 'defaults'],
    queryFn: () => riskGet<Defaults>('defaults'),
  })
  const config = useQuery({
    queryKey: ['risk', 'config'],
    queryFn: () => riskGet<ConfigView>('config'),
  })
  const directory = useQuery({
    queryKey: ['risk', 'groups'],
    queryFn: () => riskGet<Directory>('groups'),
  })
  const policies = useQuery({
    queryKey: ['risk', 'policies'],
    queryFn: () => riskGet<PolicyRow[]>('policies'),
  })
  const providers = useQuery({
    queryKey: ['risk', 'providers'],
    queryFn: () => riskGet<Provider[]>('providers'),
  })
  const pending =
    defaults.isPending ||
    config.isPending ||
    directory.isPending ||
    policies.isPending ||
    providers.isPending
  const failed =
    defaults.isError ||
    config.isError ||
    directory.isError ||
    policies.isError ||
    providers.isError
  if (pending) return <LoadingState />
  if (
    !defaults.data ||
    !config.data ||
    !directory.data ||
    !policies.data ||
    !providers.data
  ) {
    return (
      <ErrorState
        onRetry={() => void client.invalidateQueries({ queryKey: ['risk'] })}
      />
    )
  }
  return (
    <RiskAuditWorkspace
      defaults={defaults.data}
      config={config.data}
      directory={directory.data}
      policies={policies.data}
      providers={providers.data}
      failed={failed}
    />
  )
}

function RiskAuditWorkspace(props: {
  defaults: Defaults
  config: ConfigView
  directory: Directory
  policies: PolicyRow[]
  providers: Provider[]
  failed: boolean
}) {
  const { t } = useTranslation()
  const confirmDiscard = useConfirmRiskDiscard()
  const search = useSearch({ from: '/_authenticated/risk-audit/' })
  const navigate = useNavigate({ from: '/risk-audit/' })
  const user = useAuthStore((s) => s.auth.user)
  const canWrite = hasPermission(user, 'risk', 'write')
  const canUnblock = hasPermission(user, 'risk', 'unblock')
  const client = useQueryClient()
  const [scopeDirty, setScopeDirty] = useState(false)
  const [editorEpoch, setEditorEpoch] = useState(0)
  const [selection, setSelection] = useState(() => ({
    key: props.config.payload.default_binding.policy_key,
    revision:
      props.policies.find(
        (p) => p.policy_key === props.config.payload.default_binding.policy_key
      )?.revision ?? props.config.payload.default_binding.policy_revision,
  }))
  const selectedKey = selection.key
  const selectedRevision = selection.revision
  const policy = useQuery({
    queryKey: ['risk', 'policy', selectedKey, selectedRevision],
    queryFn: () =>
      riskGet<PolicyRow & { decoded: Policy }>(
        `policies/${encodeURIComponent(selectedKey)}/revisions/${selectedRevision}`
      ),
    enabled: !['all', 'pass'].includes(search.view),
  })
  const reload = useMutation({
    meta: { errorToast: false },
    mutationFn: async () => {
      const [config, policies] = await Promise.all([
        client.fetchQuery({
          queryKey: ['risk', 'config'],
          queryFn: () => riskGet<ConfigView>('config'),
          staleTime: 0,
        }),
        client.fetchQuery({
          queryKey: ['risk', 'policies'],
          queryFn: () => riskGet<PolicyRow[]>('policies'),
          staleTime: 0,
        }),
      ])
      const key = config.payload.default_binding.policy_key
      const revision =
        policies.find((p) => p.policy_key === key)?.revision ??
        config.payload.default_binding.policy_revision
      await client.fetchQuery({
        queryKey: ['risk', 'policy', key, revision],
        queryFn: () =>
          riskGet<PolicyRow & { decoded: Policy }>(
            `policies/${encodeURIComponent(key)}/revisions/${revision}`
          ),
        staleTime: 0,
      })
      return { key, revision }
    },
    onSuccess: (next) => {
      setSelection(next)
      setEditorEpoch((old) => old + 1)
    },
  })
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Risk Audit')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-5'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Enabled checks identify signals for model review. Only confirmed violations affect reputation; request blocking and token suspension are separate outcomes. Select a stage to inspect its events and settings.'
            )}
          </p>
          {props.failed && (
            <ErrorState
              onRetry={() =>
                void client.invalidateQueries({ queryKey: ['risk'] })
              }
            />
          )}
          <div className='flex flex-wrap items-end justify-between gap-3'>
            <div className='w-full sm:w-64'>
              <Choice
                label='Group filter'
                value={search.group}
                options={[
                  { value: '', label: 'All groups' },
                  ...props.directory.groups.map((g) => ({
                    value: g.group_name,
                    label: g.group_name,
                  })),
                ]}
                onChange={(group) =>
                  void navigate({ search: { ...search, group } })
                }
              />
            </div>
            <div className='flex flex-wrap gap-2'>
              {canWrite && <PreviewDialog directory={props.directory} />}
              <Button
                variant='outline'
                onClick={() =>
                  void client.invalidateQueries({ queryKey: ['risk'] })
                }
              >
                {t('Refresh')}
              </Button>
            </div>
          </div>
          <ScopeCard
            onDirtyChange={setScopeDirty}
            config={props.config}
            directory={props.directory}
            canWrite={canWrite}
          />
          <RiskOverview
            group={search.group}
            view={search.view}
            onView={(view) => void navigate({ search: { ...search, view } })}
          />
          <Accordion className='rounded-xl border px-4'>
            <AccordionItem value='reviews'>
              <AccordionTrigger>
                {t('Review and user reputation')}
              </AccordionTrigger>
              <AccordionContent>
                <ReviewRecords
                  directory={props.directory}
                  canWrite={canWrite}
                  canUnblock={canUnblock}
                />
              </AccordionContent>
            </AccordionItem>
          </Accordion>
          {search.view === 'model' && (
            <section className='space-y-4'>
              {props.providers.map((provider) => (
                <ProviderCard
                  key={provider.id}
                  provider={provider}
                  presets={props.defaults.providers}
                  canWrite={canWrite}
                  onSaved={() => {}}
                />
              ))}
              {canWrite && (
                <Accordion
                  defaultValue={props.providers.length ? [] : ['new-provider']}
                >
                  <AccordionItem value='new-provider'>
                    <AccordionTrigger>
                      {t('New decision provider')}
                    </AccordionTrigger>
                    <AccordionContent keepMounted>
                      <ProviderCard
                        presets={props.defaults.providers}
                        canWrite
                        onSaved={() => {}}
                      />
                    </AccordionContent>
                  </AccordionItem>
                </Accordion>
              )}
            </section>
          )}
          {!['all', 'pass'].includes(search.view) && (
            <section className='space-y-4'>
              <ValidationErrors error={reload.error} />
              {policy.isPending && <LoadingState />}
              {policy.isError && (
                <ErrorState onRetry={() => void policy.refetch()} />
              )}
              {policy.data && (
                <PolicySettings
                  key={`${policy.data.policy_key}-${policy.data.revision}-${search.view}-${editorEpoch}`}
                  row={policy.data}
                  initial={policy.data.decoded}
                  defaults={props.defaults}
                  providers={props.providers}
                  view={search.view}
                  canWrite={canWrite}
                  config={props.config}
                  activationBlocked={scopeDirty}
                  loadingLatest={reload.isPending}
                  onReload={() => confirmDiscard(() => reload.mutate())}
                  onSaved={(row) =>
                    setSelection({
                      key: row.policy_key,
                      revision: row.revision,
                    })
                  }
                />
              )}
            </section>
          )}
          {search.view === 'refusal' && (
            <MemoryRecords
              directory={props.directory}
              canWrite={canWrite}
              canUnblock={canUnblock}
            />
          )}
          <Accordion className='rounded-xl border px-4'>
            <AccordionItem value='coverage'>
              <AccordionTrigger>{t('Coverage')}</AccordionTrigger>
              <AccordionContent>
                <p className='my-2'>
                  {t('Not covered does not mean approved.')}
                </p>
                <p>
                  {t('Covered routes')}:{' '}
                  {props.defaults.covered_routes.join(', ')}
                </p>
                <p>
                  {t('Uncovered routes')}:{' '}
                  {props.defaults.uncovered_routes.join(', ')}
                </p>
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
