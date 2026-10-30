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
import { Button } from '@/components/ui/button'
import { TitledCard } from '@/components/ui/titled-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { riskGet } from '../api'
import { LABELS } from '../constants'
import type { Overview } from '../types'

export function RiskOverview(props: {
  group: string
  onOpenViolations: () => void
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
  const count = (...outcomes: string[]) =>
    outcomes.reduce((sum, outcome) => sum + (data.outcomes[outcome] ?? 0), 0)
  const stats = [
    {
      label: 'Today requests',
      value: data.requests,
      caption: t('Audit attempts: {{count}}', {
        count: formatNumber(data.attempts, locale),
      }),
    },
    {
      label: 'Blocked',
      value: count('block', 'error_block'),
      caption: t('Blocked on error: {{count}}', {
        count: formatNumber(count('error_block'), locale),
      }),
    },
    {
      label: 'Observation hit',
      value: count('would_block'),
      caption: t('Recorded without rejecting the request'),
    },
    {
      label: 'Passed',
      value: count('pass', 'error_pass', 'exempt'),
      caption: t('Not covered: {{count}}', {
        count: formatNumber(count('uncovered'), locale),
      }),
    },
  ]
  const stages = data.enabled ? data.stages : { keyword: 'off', model: 'off' }
  return (
    <div className='min-w-0 space-y-4'>
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
      <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-5'>
        {stats.map((stat) => (
          <section
            key={stat.label}
            aria-label={t(stat.label)}
            className='bg-card space-y-2 rounded-xl border p-4'
          >
            <p className='text-muted-foreground text-sm'>{t(stat.label)}</p>
            <p className='text-2xl font-semibold tabular-nums'>
              {formatNumber(stat.value, locale)}
            </p>
            <p className='text-muted-foreground text-xs'>{stat.caption}</p>
          </section>
        ))}
        <section
          aria-label={t('Pending review')}
          className='bg-card space-y-2 rounded-xl border p-4'
        >
          <p className='text-muted-foreground text-sm'>{t('Pending review')}</p>
          <p className='text-2xl font-semibold tabular-nums'>
            {formatNumber(data.pending_reviews, locale)}
          </p>
          <Button variant='outline' size='sm' onClick={props.onOpenViolations}>
            {t('Review violations')}
          </Button>
        </section>
      </div>
      <div className='text-muted-foreground flex flex-wrap items-center gap-3 text-sm'>
        <span>{t('Today, server time')}</span>
        {(['keyword', 'model'] as const).map((stage) => (
          <span key={stage} className='flex items-center gap-2'>
            {t(LABELS[stage])}
            <Badge variant='outline'>
              {t(LABELS[stages[stage] ?? 'off'] ?? stages[stage])}
            </Badge>
          </span>
        ))}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Requests without a keyword match pass and are only counted. Stage counts are audit attempts, including retries.'
        )}
        {!props.group &&
          ` ${t('Stage status follows the default policy when all groups are selected.')}`}
      </p>
      <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
        <TitledCard
          title={t('Top hits')}
          disableHoverEffect
          className='min-w-0'
          titleClassName='text-base sm:text-base'
        >
          <StaticDataTable
            data={[...data.blocks, ...data.passes]}
            emptyContent={t('No data')}
            columns={[
              {
                id: 'layer',
                header: t('Layer'),
                cell: (r) => t(LABELS[r.layer] ?? r.layer),
              },
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
        </TitledCard>
        <TitledCard
          title={t('Top keywords')}
          disableHoverEffect
          className='min-w-0'
          titleClassName='text-base sm:text-base'
        >
          <StaticDataTable
            data={data.keywords ?? []}
            emptyContent={t('No data')}
            columns={[
              {
                id: 'keyword',
                header: t('Keyword'),
                cell: (r) => <span className='break-all'>{r.keyword}</span>,
              },
              {
                id: 'layer',
                header: t('Layer'),
                cell: (r) => t(LABELS[r.layer] ?? r.layer),
              },
              {
                id: 'count',
                header: t('Count'),
                cell: (r) => formatNumber(r.count, locale),
              },
            ]}
          />
        </TitledCard>
      </div>
    </div>
  )
}
