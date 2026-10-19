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

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet } from '../api'
import { EVENT_LABELS, LABELS } from '../constants'
import type { Page, RiskEvent } from '../types'
import { Choice } from './fields'

export function EventFeed(props: {
  group: string
  layer: string
  compact?: boolean
  startAt?: number
  upstreamOnly?: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [page, setPage] = useState(1)
  const [outcome, setOutcome] = useState('')
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const [detail, setDetail] = useState<number | null>(null)
  const [showFilters, setShowFilters] = useState(!props.compact)
  const pageSize = props.compact ? 5 : 20
  const filters = {
    group: props.group,
    layer: props.layer,
    outcome: props.upstreamOnly ? '' : outcome,
    p: page,
    page_size: pageSize,
    start: start ? Math.floor(new Date(start).getTime() / 1000) : props.startAt,
    end: end ? Math.floor(new Date(end).getTime() / 1000) : undefined,
  }
  const query = useQuery({
    queryKey: ['risk', 'events', filters],
    queryFn: () => riskGet<Page<RiskEvent>>('events', filters),
    refetchInterval: 30_000,
  })
  const eventQuery = useQuery({
    queryKey: ['risk', 'event', detail],
    queryFn: () => riskGet<RiskEvent>(`events/${detail}`),
    enabled: detail !== null,
  })
  let title = props.compact ? 'Recent events' : 'Risk events'
  if (props.upstreamOnly) title = 'Upstream refusal'
  return (
    <TitledCard
      title={t(title)}
      disableHoverEffect
      className='min-w-0'
      titleClassName='text-base sm:text-base'
      contentClassName='space-y-3'
      action={
        props.compact && (
          <Button
            variant='ghost'
            size='sm'
            aria-expanded={showFilters}
            onClick={() => setShowFilters(!showFilters)}
          >
            {t('Filters')}
          </Button>
        )
      }
    >
      {showFilters && (
        <div className='grid gap-3 sm:grid-cols-3'>
          {!props.upstreamOnly && (
            <Choice
              label='Outcome'
              value={outcome}
              options={[
                '',
                'block',
                'would_block',
                'error_block',
                'error_pass',
                'upstream_refusal',
                'would_stop',
              ].map((v) => ({ value: v, label: LABELS[v] ?? 'All' }))}
              onChange={(v) => {
                setOutcome(v)
                setPage(1)
              }}
            />
          )}
          <label className='space-y-2 text-sm'>
            {t('Start time')}
            <Input
              type='datetime-local'
              value={start}
              onChange={(e) => {
                setStart(e.target.value)
                setPage(1)
              }}
            />
          </label>
          <label className='space-y-2 text-sm'>
            {t('End time')}
            <Input
              type='datetime-local'
              value={end}
              onChange={(e) => {
                setEnd(e.target.value)
                setPage(1)
              }}
            />
          </label>
        </div>
      )}
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {query.data && (
        <>
          <StaticDataTable
            data={query.data.items ?? []}
            getRowKey={(e) => e.id}
            emptyContent={t('No risk events in this range.')}
            columns={[
              {
                id: 'time',
                header: t('Time'),
                cell: (e) => (
                  <div>
                    <span>
                      {new Date(e.created_at * 1000).toLocaleString(locale)}
                    </span>
                    {props.compact && (
                      <p className='text-muted-foreground text-xs'>
                        {e.effective_group || 'auto'} · {t('User ID')}{' '}
                        {formatNumber(e.user_id, locale)}
                      </p>
                    )}
                  </div>
                ),
              },
              ...(!props.compact
                ? [
                    {
                      id: 'group',
                      header: t('Group'),
                      cell: (e: RiskEvent) => e.effective_group || 'auto',
                    },
                    {
                      id: 'user',
                      header: t('User ID'),
                      cell: (e: RiskEvent) => formatNumber(e.user_id, locale),
                    },
                    {
                      id: 'layer',
                      header: t('Layer'),
                      cell: (e: RiskEvent) => t(LABELS[e.layer] ?? e.layer),
                    },
                  ]
                : []),
              {
                id: 'outcome',
                header: t('Outcome'),
                cell: (e) => (
                  <div>
                    <span>{t(LABELS[e.outcome] ?? e.outcome)}</span>
                    {props.compact && (
                      <p className='text-muted-foreground text-xs'>
                        {t(LABELS[e.layer] ?? e.layer)}
                      </p>
                    )}
                  </div>
                ),
              },
              {
                id: 'details',
                header: t('Details'),
                cell: (e) => (
                  <Button variant='ghost' onClick={() => setDetail(e.id)}>
                    {t('View details')}
                  </Button>
                ),
              },
            ]}
          />
          <div className='flex items-center justify-end gap-3'>
            <Button
              variant='outline'
              disabled={page === 1}
              onClick={() => setPage(page - 1)}
            >
              {t('Previous')}
            </Button>
            <span>{formatNumber(page, locale)}</span>
            <Button
              variant='outline'
              disabled={page * pageSize >= query.data.total}
              onClick={() => setPage(page + 1)}
            >
              {t('Next')}
            </Button>
          </div>
        </>
      )}
      <Dialog
        open={detail !== null}
        onOpenChange={(open) => {
          if (!open) setDetail(null)
        }}
        title={t('Risk event details')}
      >
        {eventQuery.isPending && <LoadingState />}
        {eventQuery.isError && (
          <ErrorState onRetry={() => void eventQuery.refetch()} />
        )}
        {eventQuery.data && (
          <div className='space-y-4'>
            <div className='flex items-center gap-2'>
              <code className='break-all'>{eventQuery.data.request_id}</code>
              <CopyButton value={eventQuery.data.request_id} />
            </div>
            <dl className='grid gap-3 sm:grid-cols-2'>
              {Object.entries(eventQuery.data)
                .filter(
                  ([key]) => !['preview', 'rule', 'request_id'].includes(key)
                )
                .map(([key, value]) => (
                  <div key={key}>
                    <dt className='text-muted-foreground text-xs'>
                      {t(EVENT_LABELS[key] ?? key)}
                    </dt>
                    <dd className='text-sm break-all'>
                      {typeof value === 'number'
                        ? formatNumber(value, locale)
                        : t(LABELS[String(value)] ?? String(value))}
                    </dd>
                  </div>
                ))}
            </dl>
            <section>
              <h4>{t('Matched rule')}</h4>
              <pre className='break-all whitespace-pre-wrap'>
                {eventQuery.data.rule}
              </pre>
            </section>
            <section>
              <h4>{t('Evidence preview')}</h4>
              <p className='break-all whitespace-pre-wrap'>
                {eventQuery.data.preview || '—'}
              </p>
            </section>
          </div>
        )}
      </Dialog>
    </TitledCard>
  )
}
