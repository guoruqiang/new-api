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
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet } from '../api'
import { LABELS } from '../constants'
import type { Page, Violation } from '../types'
import { Choice, Field } from './fields'
import { ViolationDetail } from './violation-detail'

const PAGE_SIZE = 20

export function ViolationRecords(props: {
  canWrite: boolean
  canUnblock: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [status, setStatus] = useState('pending')
  const [user, setUser] = useState('')
  const [request, setRequest] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<number | null>(null)
  const filters = {
    status,
    user_id: user || undefined,
    request_id: request.trim() || undefined,
    p: page,
    page_size: PAGE_SIZE,
  }
  const query = useQuery({
    queryKey: ['risk', 'violations', filters],
    queryFn: () => riskGet<Page<Violation>>('violations', filters),
  })
  return (
    <TitledCard
      title={t('Violations')}
      description={t(
        'Each record is a request the decision model judged as a violation. Review the saved request, uphold or revoke the deduction, and ban or unban the account when needed.'
      )}
      disableHoverEffect
      className='min-w-0'
      contentClassName='space-y-4'
    >
      <div className='grid gap-3 sm:grid-cols-3'>
        <Choice
          label='Review status'
          value={status}
          options={[
            { value: '', label: 'All statuses' },
            ...['pending', 'upheld', 'revoked'].map((value) => ({
              value,
              label: LABELS[value],
            })),
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
          tableClassName='min-w-[56rem]'
          tableProps={{ 'aria-label': t('Violations') }}
          data={query.data.items ?? []}
          getRowKey={(row) => row.id}
          emptyContent={t('No violations')}
          columns={[
            {
              id: 'time',
              header: t('Time'),
              cell: (row) =>
                new Date(row.created_at * 1000).toLocaleString(locale),
            },
            {
              id: 'user',
              header: t('User ID'),
              cell: (row) => formatNumber(row.user_id, locale),
            },
            {
              id: 'token',
              header: t('Token ID'),
              cell: (row) =>
                row.token_id ? formatNumber(row.token_id, locale) : '—',
            },
            {
              id: 'group',
              header: t('Group'),
              cell: (row) => row.group_name || 'auto',
            },
            {
              id: 'category',
              header: t('Category'),
              cell: (row) => t(LABELS[row.category] ?? row.category) || '—',
            },
            {
              id: 'keyword',
              header: t('Keyword'),
              cell: (row) => (
                <span className='break-all'>{row.keyword || '—'}</span>
              ),
            },
            {
              id: 'deduction',
              header: t('Score change'),
              cell: (row) =>
                `${formatNumber(row.score_before, locale)} → ${formatNumber(row.score_after, locale)}`,
            },
            {
              id: 'action',
              header: t('Action'),
              cell: (row) => t(LABELS[row.action] ?? row.action),
            },
            {
              id: 'status',
              header: t('Review status'),
              cell: (row) => (
                <Badge variant={row.status === 'pending' ? 'default' : 'secondary'}>
                  {t(LABELS[row.status] ?? row.status)}
                </Badge>
              ),
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
          disabled={!query.data || page * PAGE_SIZE >= query.data.total}
          onClick={() => setPage(page + 1)}
        >
          {t('Next')}
        </Button>
      </div>
      {selected !== null && (
        <ViolationDetail
          id={selected}
          canWrite={props.canWrite}
          canUnblock={props.canUnblock}
          onClose={() => setSelected(null)}
        />
      )}
    </TitledCard>
  )
}
