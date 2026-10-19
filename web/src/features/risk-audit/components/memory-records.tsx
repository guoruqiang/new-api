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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet, riskWrite } from '../api'
import { LABELS } from '../constants'
import type { Directory, MemoryRow, Page } from '../types'
import { Choice, Toggle } from './fields'

export function MemoryRecords(props: {
  directory: Directory
  canWrite: boolean
  canUnblock: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [namespace, setNamespace] = useState('')
  const [kind, setKind] = useState('refusals')
  const [user, setUser] = useState('')
  const [model, setModel] = useState('')
  const [active, setActive] = useState(true)
  const [page, setPage] = useState(1)
  const [action, setAction] = useState<{
    path: string
    method: 'post' | 'delete'
    data?: unknown
  } | null>(null)
  const query = useQuery({
    queryKey: ['risk', kind, namespace, user, model, active, page],
    queryFn: () =>
      riskGet<Page<MemoryRow>>(kind, {
        namespace,
        user_id: kind === 'refusals' && user ? user : undefined,
        model: kind === 'refusals' && model ? model : undefined,
        active,
        p: page,
        page_size: 20,
      }),
    enabled: !!namespace,
  })
  const write = useMutation({
    mutationFn: (pending: NonNullable<typeof action>) =>
      riskWrite(pending.method, pending.path, pending.data),
    onSuccess: () => {
      setAction(null)
      void client.invalidateQueries({ queryKey: ['risk', kind] })
    },
  })
  const allowed = kind === 'blocks' ? props.canUnblock : props.canWrite
  return (
    <section className='space-y-4 rounded-xl border p-4'>
      <h3 className='font-semibold'>
        {t('Refusal records and subject blocks')}
      </h3>
      <div className='grid gap-3 sm:grid-cols-3'>
        <Choice
          label='Group namespace'
          value={namespace}
          options={[
            { value: '', label: 'Select a group' },
            { value: 'auto', label: 'auto' },
            ...props.directory.groups.map((g) => ({
              value: g.namespace,
              label: g.group_name,
            })),
          ]}
          onChange={(v) => {
            setNamespace(v)
            setPage(1)
          }}
        />
        <Choice
          label='Records'
          value={kind}
          options={[
            { value: 'refusals', label: 'Refusal memory' },
            { value: 'blocks', label: 'Subject blocks' },
          ]}
          onChange={(v) => {
            setKind(v)
            setPage(1)
          }}
        />
        {kind === 'refusals' && (
          <label className='space-y-2 text-sm'>
            {t('User ID')}
            <Input
              type='number'
              min={1}
              value={user}
              onChange={(e) => {
                setUser(e.target.value)
                setPage(1)
              }}
            />
          </label>
        )}
        {kind === 'refusals' && (
          <label className='space-y-2 text-sm'>
            {t('Model')}
            <Input
              value={model}
              onChange={(e) => {
                setModel(e.target.value)
                setPage(1)
              }}
            />
          </label>
        )}
      </div>
      {kind === 'blocks' && (
        <Toggle
          label='Active blocks only'
          checked={active}
          onChange={(v) => {
            setActive(v)
            setPage(1)
          }}
        />
      )}
      {namespace && (
        <>
          {query.isPending && <LoadingState />}
          {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
          {query.data && (
            <StaticDataTable
              data={query.data.items ?? []}
              getRowKey={(r) => r.id}
              emptyContent={t('No data')}
              columns={[
                {
                  id: 'subject',
                  header: t('Subject'),
                  cell: (r) =>
                    kind === 'blocks'
                      ? `${t(LABELS[r.subject_type] ?? r.subject_type)}: ${r.subject_value}`
                      : formatNumber(r.user_id, locale),
                },
                {
                  id: 'reason',
                  header: t('Reason'),
                  cell: (r) =>
                    r.error_message || t(LABELS[r.reason] ?? r.reason),
                },
                {
                  id: 'preview',
                  header: t('Evidence preview'),
                  cell: (r) => r.preview || '—',
                },
                {
                  id: 'hits',
                  header: t('Hits'),
                  cell: (r) => formatNumber(r.hit_count, locale),
                },
                {
                  id: 'expiry',
                  header: t('Expires at'),
                  cell: (r) =>
                    r.expires_at
                      ? new Date(r.expires_at * 1000).toLocaleString(locale)
                      : t('Permanent'),
                },
                {
                  id: 'action',
                  header: t('Actions'),
                  cell: (r) =>
                    allowed &&
                    (kind !== 'blocks' || !r.released_at) && (
                      <Button
                        variant='outline'
                        onClick={() =>
                          setAction({
                            method: kind === 'blocks' ? 'post' : 'delete',
                            path:
                              kind === 'blocks'
                                ? `blocks/${r.id}/release`
                                : `refusals/${r.id}`,
                          })
                        }
                      >
                        {t(kind === 'blocks' ? 'Release block' : 'Delete')}
                      </Button>
                    ),
                },
              ]}
            />
          )}
          <div className='flex flex-wrap justify-between gap-2'>
            {allowed && (
              <Button
                variant='destructive'
                onClick={() =>
                  setAction({
                    method: 'post',
                    path: `${kind}/clear`,
                    data: {
                      namespace,
                      confirm: true,
                      ...(kind === 'refusals' && user
                        ? { user_id: Number(user) }
                        : {}),
                    },
                  })
                }
              >
                {t('Clear selected group')}
              </Button>
            )}
            <div className='flex gap-2'>
              <Button
                variant='outline'
                disabled={page === 1}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                variant='outline'
                disabled={!query.data || page * 20 >= query.data.total}
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          </div>
        </>
      )}
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !write.isPending) setAction(null)
        }}
        title={t('Confirm risk record change')}
        desc={
          <div className='space-y-2'>
            <p>
              {t(
                action?.path.endsWith('/clear')
                  ? 'This affects the entire selected group, across all pages and models.'
                  : 'This action affects only the selected record.'
              )}
            </p>
            <p>
              {t('Group namespace')}: {namespace}
              {kind === 'refusals' && user ? ` · ${t('User ID')}: ${user}` : ''}
            </p>
          </div>
        }
        destructive
        isLoading={write.isPending}
        handleConfirm={() => {
          if (action) write.mutate(action)
        }}
      />
    </section>
  )
}
