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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { riskGet } from './api'
import { EventFeed } from './components/event-feed'
import { Choice } from './components/fields'
import { RiskOverview } from './components/overview'
import { PolicySettings } from './components/policy-settings'
import { PreviewDialog } from './components/preview-dialog'
import { ProviderCard } from './components/provider-card'
import { ScopeCard } from './components/scope-card'
import { ValidationErrors } from './components/validation-errors'
import { ViolationRecords } from './components/violation-records'
import {
  LABELS,
  POLICY_SECTIONS,
  VIEW_LABELS,
  VIEWS,
  type PolicySection,
  type View,
} from './constants'
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
  // Views are local state: switching tabs must not navigate, or the draft
  // guard would ask to discard unsaved settings kept in the other tabs.
  const [view, setView] = useState<View>(search.view)
  const [section, setSection] = useState<PolicySection>(search.section)
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
              'Requests are screened by keyword rules and distillation detection. Requests without a match pass and are only counted; matches are reviewed by the decision model, and only a confirmed violation is rejected and deducts the user score.'
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
          <Tabs
            value={view}
            onValueChange={(next) => setView(next as View)}
          >
            <TabsList className='h-auto w-full flex-wrap justify-start'>
              {VIEWS.map((view) => (
                <TabsTrigger key={view} value={view}>
                  {t(VIEW_LABELS[view])}
                </TabsTrigger>
              ))}
            </TabsList>
            <TabsContent value='overview' className='mt-4'>
              <RiskOverview
                group={search.group}
                onOpenViolations={() => setView('violations')}
              />
            </TabsContent>
            <TabsContent value='scope' keepMounted className='mt-4'>
              <ScopeCard
                onDirtyChange={setScopeDirty}
                config={props.config}
                directory={props.directory}
                scoreCategories={props.defaults.score_categories}
                canWrite={canWrite}
              />
            </TabsContent>
            <TabsContent value='policy' keepMounted className='mt-4 space-y-4'>
              <Tabs
                value={section}
                onValueChange={(next) => setSection(next as PolicySection)}
              >
                <TabsList>
                  {POLICY_SECTIONS.map((section) => (
                    <TabsTrigger key={section} value={section}>
                      {t(LABELS[section])}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
              <ValidationErrors error={reload.error} />
              {policy.isPending && <LoadingState />}
              {policy.isError && (
                <ErrorState onRetry={() => void policy.refetch()} />
              )}
              {policy.data && (
                <PolicySettings
                  key={`${policy.data.policy_key}-${policy.data.revision}-${editorEpoch}`}
                  row={policy.data}
                  initial={policy.data.decoded}
                  defaults={props.defaults}
                  providers={props.providers}
                  section={section}
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
            </TabsContent>
            <TabsContent value='providers' keepMounted className='mt-4 space-y-4'>
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
            </TabsContent>
            <TabsContent value='violations' className='mt-4'>
              <ViolationRecords canWrite={canWrite} canUnblock={canUnblock} />
            </TabsContent>
            <TabsContent value='events' className='mt-4'>
              <EventFeed group={search.group} />
            </TabsContent>
          </Tabs>
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
