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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet } from '../api'
import { LABELS, VIEWS, type View } from '../constants'
import type { Overview } from '../types'
import { EventFeed } from './event-feed'

export function GateRail(props: {
  view: View
  states: Record<string, string>
  metrics?: Record<View, { count: number; caption: string }>
  onChange: (v: View) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <Tabs value={props.view} onValueChange={(v) => props.onChange(v as View)}>
      <TabsList className='grid h-auto w-full grid-cols-2 items-stretch gap-1 group-data-horizontal/tabs:h-auto sm:grid-cols-3 xl:grid-cols-6'>
        {VIEWS.map((view) => (
          <TabsTrigger
            key={view}
            value={view}
            aria-label={[
              t(LABELS[view]),
              props.states[view]
                ? t(LABELS[props.states[view]] ?? props.states[view])
                : '',
            ]
              .filter(Boolean)
              .join(' ')}
            className='h-auto min-w-0 flex-col items-stretch justify-start gap-3 p-3 text-start whitespace-normal sm:p-4'
          >
            <span className='flex min-w-0 flex-wrap items-center justify-between gap-2'>
              <span>{t(view === 'all' ? 'Today requests' : LABELS[view])}</span>
              {props.states[view] && (
                <Badge variant='outline'>
                  {t(LABELS[props.states[view]] ?? props.states[view])}
                </Badge>
              )}
            </span>
            {props.metrics && (
              <>
                <span className='text-foreground text-2xl font-semibold tabular-nums'>
                  {formatNumber(props.metrics[view].count, locale)}
                </span>
                <span className='text-muted-foreground text-xs font-normal'>
                  {props.metrics[view].caption}
                </span>
              </>
            )}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}

export function RiskOverview(props: {
  group: string
  view: View
  onView: (v: View) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const query = useQuery({
    queryKey: ['risk', 'overview', props.group],
    queryFn: () => riskGet<Overview>('overview', { group: props.group }),
    refetchInterval: 30_000,
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  const data = query.data
  const passed = ['pass', 'error_pass', 'exempt', 'would_block'].reduce(
    (sum, outcome) => sum + (data.outcomes[outcome] ?? 0),
    0
  )
  const metrics = Object.fromEntries(
    VIEWS.map((view) => {
      let count = data.blocks
        .filter(
          (r) =>
            r.layer === view && ['block', 'error_block'].includes(r.outcome)
        )
        .reduce((sum, r) => sum + r.count, 0)
      if (view === 'all') count = data.requests
      if (view === 'pass') count = passed
      return [
        view,
        {
          count,
          caption:
            view === 'all'
              ? t('Audit attempts: {{count}}', {
                  count: formatNumber(data.attempts, locale),
                })
              : `${t(view === 'pass' ? 'Passed' : 'Blocked')} · ${t(
                  'Share of audit attempts: {{percent}}%',
                  {
                    percent: formatNumber(
                      data.attempts ? (count / data.attempts) * 100 : 0,
                      locale
                    ),
                  }
                )}`,
        },
      ]
    })
  ) as Record<View, { count: number; caption: string }>
  const stageHits = data.blocks.filter((r) => r.layer === props.view)
  let eventLayer: string = props.view
  if (props.view === 'all') eventLayer = ''
  if (props.view === 'pass') eventLayer = 'upstream'
  return (
    <div className='space-y-4'>
      {(!data.fresh || !data.available) && (
        <p role='alert' className='text-destructive'>
          {t('Risk configuration is unavailable or stale.')}
        </p>
      )}
      {!data.enabled && (
        <p className='text-muted-foreground text-sm'>
          {t('Risk audit is disabled. Requests are not counted.')}
        </p>
      )}
      <GateRail
        view={props.view}
        metrics={metrics}
        states={
          data.enabled
            ? (data.stages ?? {})
            : {
                distill: 'off',
                hard_regex: 'off',
                refusal: 'off',
                model: 'off',
              }
        }
        onChange={props.onView}
      />
      <div className='space-y-2'>
        <div className='text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-2 text-xs'>
          <span>{t('Today, server time')}</span>
          <span>
            {t('Observation hit')}:{' '}
            {formatNumber(data.outcomes.would_block ?? 0, locale)}
          </span>
          <span>
            {t('Model failure allowed')}:{' '}
            {formatNumber(data.outcomes.error_pass ?? 0, locale)}
          </span>
          <span>
            {t('Not covered')}:{' '}
            {formatNumber(data.outcomes.uncovered ?? 0, locale)}
          </span>
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Stage counts are audit attempts, including retries. Observation hits and uncovered requests are listed separately.'
          )}
        </p>
        {!props.group && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Stage status follows the default policy when all groups are selected.'
            )}
          </p>
        )}
      </div>
      <div className='grid min-w-0 gap-4 xl:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]'>
        <TitledCard
          title={t(props.view === 'pass' ? 'Pass reasons' : 'Top hits')}
          disableHoverEffect
          className='min-w-0'
          titleClassName='text-base sm:text-base'
        >
          {props.view === 'pass' && (
            <StaticDataTable
              data={[
                ...data.passes.filter((r) =>
                  ['pass', 'error_pass', 'exempt'].includes(r.outcome)
                ),
                ...data.blocks.filter((r) => r.outcome === 'would_block'),
              ]}
              emptyContent={t('No data')}
              columns={[
                {
                  id: 'reason',
                  header: t('Reason'),
                  cell: (r) => t(LABELS[r.reason] ?? r.reason),
                },
                {
                  id: 'count',
                  header: t('Count'),
                  cell: (r) => formatNumber(r.count, locale),
                },
                {
                  id: 'outcome',
                  header: t('Outcome'),
                  cell: (r) => t(LABELS[r.outcome] ?? r.outcome),
                },
              ]}
            />
          )}
          {props.view === 'all' && (
            <StaticDataTable
              data={data.keywords ?? []}
              emptyContent={t('No data')}
              columns={[
                {
                  id: 'layer',
                  header: t('Layer'),
                  cell: (r) => t(LABELS[r.layer] ?? r.layer),
                },
                { id: 'keyword', header: t('Keyword'), cell: (r) => r.keyword },
                {
                  id: 'count',
                  header: t('Count'),
                  cell: (r) => formatNumber(r.count, locale),
                },
              ]}
            />
          )}
          {!['all', 'pass'].includes(props.view) && (
            <StaticDataTable
              data={stageHits}
              emptyContent={t('No data')}
              columns={[
                {
                  id: 'reason',
                  header: t('Reason'),
                  cell: (r) => t(LABELS[r.reason] ?? r.reason),
                },
                {
                  id: 'outcome',
                  header: t('Outcome'),
                  cell: (r) => t(LABELS[r.outcome] ?? r.outcome),
                },
                {
                  id: 'count',
                  header: t('Count'),
                  cell: (r) => formatNumber(r.count, locale),
                },
              ]}
            />
          )}
        </TitledCard>
        <EventFeed
          key={`${props.group}-${props.view}`}
          group={props.group}
          layer={eventLayer}
          compact
          startAt={data.start}
          upstreamOnly={props.view === 'pass'}
        />
      </div>
    </div>
  )
}
