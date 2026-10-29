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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet } from '../api'
import { JUDGMENT_SOURCE_LABELS, LABELS } from '../constants'
import type { Directory, Page, RiskReview } from '../types'
import { Choice, Field } from './fields'
import { ReviewDetail } from './review-detail'

export function ReviewRecords(props: {
  directory: Directory
  canWrite: boolean
  canUnblock: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [namespace, setNamespace] = useState('')
  const [status, setStatus] = useState('pending')
  const [user, setUser] = useState('')
  const [request, setRequest] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const query = useQuery({
    queryKey: ['risk', 'reviews', namespace, status, user, request, page],
    queryFn: () =>
      riskGet<Page<RiskReview>>('reviews', {
        namespace,
        status,
        user_id: user || undefined,
        request_id: request || undefined,
        p: page,
        page_size: 20,
      }),
  })
  return (
    <div className='space-y-4 py-2'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Candidates require review before scoring. Confirmed explicit content and jailbreak violations add 3 points; other categories add 1. Observation candidates always add 0 points.'
        )}
      </p>
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
        <Choice
          label='Group namespace'
          value={namespace}
          options={[
            { value: '', label: 'All groups' },
            { value: 'auto', label: 'auto' },
            ...props.directory.groups.map((group) => ({
              value: group.namespace,
              label: group.group_name,
            })),
          ]}
          onChange={(value) => {
            setNamespace(value)
            setPage(1)
          }}
        />
        <Choice
          label='Review status'
          value={status}
          options={[
            { value: '', label: 'All statuses' },
            ...['pending', 'confirmed', 'dismissed', 'revoked'].map(
              (value) => ({ value, label: LABELS[value] })
            ),
          ]}
          onChange={(value) => {
            setStatus(value)
            setPage(1)
          }}
        />
        <Field label='User ID'>
          <Input
            aria-label={t('User ID')}
            type='number'
            min={1}
            step={1}
            value={user}
            onChange={(event) => {
              setUser(event.target.value)
              setPage(1)
            }}
          />
        </Field>
        <Field label='Request ID'>
          <Input
            aria-label={t('Request ID')}
            value={request}
            onChange={(event) => {
              setRequest(event.target.value)
              setPage(1)
            }}
          />
        </Field>
      </div>
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.data && (
        <StaticDataTable
          className='overflow-x-auto'
          tableClassName='min-w-[42rem]'
          tableProps={{ 'aria-label': t('Review candidates') }}
          data={query.data.items ?? []}
          getRowKey={(row) => row.id}
          emptyContent={t('No review candidates')}
          columns={[
            {
              id: 'subject',
              header: t('Subject'),
              cell: (row) => (
                <div className='space-y-1'>
                  <p>
                    {row.group_name} · {t('User ID')}:{' '}
                    {formatNumber(row.user_id, locale)}
                  </p>
                  <p className='text-muted-foreground text-xs break-all'>
                    {row.namespace}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t('Token ID')}:{' '}
                    {row.token_id ? formatNumber(row.token_id, locale) : '—'}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {new Date(row.created_at * 1000).toLocaleString(locale)}
                  </p>
                </div>
              ),
            },
            {
              id: 'candidate',
              header: t('Reason'),
              cell: (row) => (
                <div>
                  <p>{t(LABELS[row.layer] ?? row.layer)}</p>
                  <p className='text-muted-foreground text-xs'>
                    {t(LABELS[row.category] ?? row.category) || '—'}
                  </p>
                </div>
              ),
            },
            {
              id: 'status',
              header: t('Review status'),
              cell: (row) => (
                <div className='space-y-1'>
                  <Badge variant='secondary'>{t(LABELS[row.status])}</Badge>
                  <p className='text-muted-foreground text-xs'>
                    {t(LABELS[row.mode])}
                  </p>
                  {row.judgment_source && (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        JUDGMENT_SOURCE_LABELS[row.judgment_source] ??
                          row.judgment_source
                      )}
                    </p>
                  )}
                </div>
              ),
            },
            {
              id: 'points',
              header: t('Confirmed points'),
              cell: (row) => formatNumber(row.points, locale),
            },
            {
              id: 'details',
              header: t('Details'),
              cell: (row) => (
                <Button variant='outline' onClick={() => setSelected(row.id)}>
                  {t('View details')}
                </Button>
              ),
            },
          ]}
        />
      )}
      <div className='flex flex-wrap items-center justify-end gap-2'>
        <Button
          variant='outline'
          disabled={page === 1}
          onClick={() => setPage(page - 1)}
        >
          {t('Previous')}
        </Button>
        <span className='text-muted-foreground text-sm'>
          {formatNumber(page, locale)}
        </span>
        <Button
          variant='outline'
          disabled={!query.data || page * 20 >= query.data.total}
          onClick={() => setPage(page + 1)}
        >
          {t('Next')}
        </Button>
      </div>
      {selected !== null && (
        <ReviewDetail
          id={selected}
          canWrite={props.canWrite}
          canUnblock={props.canUnblock}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
