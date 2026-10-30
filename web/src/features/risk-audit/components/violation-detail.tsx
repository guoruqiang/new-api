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
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { tryPrettyJson } from '@/lib/utils'

import { riskGet, riskWrite } from '../api'
import { LABELS, NOTIFICATION_LABELS, USER_STATUS_LABELS } from '../constants'
import { violationNoteSchema } from '../lib/schema'
import type { ViolationDetail as Detail } from '../types'
import { Field } from './fields'
import { ValidationErrors } from './validation-errors'

type ViolationAction = 'uphold' | 'revoke' | 'ban' | 'unban'

const ACTION_LABELS: Record<ViolationAction, string> = {
  uphold: 'Uphold violation',
  revoke: 'Revoke violation',
  ban: 'Ban account',
  unban: 'Unban account',
}

export function ViolationDetail(props: {
  id: number
  canWrite: boolean
  canUnblock: boolean
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [action, setAction] = useState<ViolationAction | null>(null)
  const [note, setNote] = useState('')
  const query = useQuery({
    queryKey: ['risk', 'violation', props.id],
    queryFn: () => riskGet<Detail>(`violations/${props.id}`),
  })
  const act = useMutation({
    meta: { errorToast: false },
    mutationFn: async (input: {
      action: ViolationAction
      userId: number
      note: string
    }) => {
      if (input.action === 'ban' || input.action === 'unban') {
        await riskWrite<null>('post', `users/${input.userId}/ban`, {
          action: input.action,
          note: input.note,
        })
        return null
      }
      return riskWrite<Detail>('post', `violations/${props.id}/resolve`, {
        action: input.action,
        note: input.note,
      })
    },
    onSuccess: (detail) => {
      setAction(null)
      setNote('')
      if (detail) client.setQueryData(['risk', 'violation', props.id], detail)
      else void query.refetch()
      void client.invalidateQueries({ queryKey: ['risk', 'violations'] })
      void client.invalidateQueries({ queryKey: ['risk', 'overview'] })
      toast.success(t('Saved successfully'))
    },
  })
  const detail = query.data
  const violation = detail?.violation
  const submitting = act.isPending
  const noteValid = violationNoteSchema.safeParse(note).success
  const body = tryPrettyJson(violation?.request_body ?? '')
  let scores: [string, number][] = []
  try {
    const parsed: unknown = JSON.parse(violation?.scores || '{}')
    if (parsed && typeof parsed === 'object') {
      scores = Object.entries(parsed).filter(
        (entry): entry is [string, number] => typeof entry[1] === 'number'
      )
    }
  } catch {
    scores = []
  }
  const available: Record<ViolationAction, boolean> = {
    uphold: props.canWrite && violation?.status === 'pending',
    revoke: props.canWrite && !!violation && violation.status !== 'revoked',
    ban: props.canUnblock && detail?.user_status === 1,
    unban: props.canUnblock && detail?.user_status === 2,
  }
  const descriptions: Record<ViolationAction, string> = {
    uphold: t(
      'Confirm that this request violated the policy. The deduction stays in effect.'
    ),
    revoke: t(
      'Revoke this violation and return its {{points}} points to the user score.',
      { points: formatNumber(violation?.deduction ?? 0, locale) }
    ),
    ban: t(
      'Disable the whole account of user {{id}}. All of its API tokens stop working until the account is unbanned. Administrator accounts cannot be banned.',
      { id: violation?.user_id ?? 0 }
    ),
    unban: t(
      'Enable the account of user {{id}} again. The score is not changed.',
      { id: violation?.user_id ?? 0 }
    ),
  }
  const fields: [string, ReactNode][] = violation
    ? [
        [
          'Time',
          new Date(violation.created_at * 1000).toLocaleString(locale),
        ],
        ['User ID', formatNumber(violation.user_id, locale)],
        [
          'Token ID',
          violation.token_id ? formatNumber(violation.token_id, locale) : '—',
        ],
        ['Group', violation.group_name || 'auto'],
        ['Group namespace', violation.namespace],
        ['Mode', t(LABELS[violation.mode] ?? violation.mode)],
        ['Model', violation.model || '—'],
        ['Category', t(LABELS[violation.category] ?? violation.category)],
        ['Keyword', violation.keyword || '—'],
        ['Decision model', violation.provider_model || '—'],
        ['Deduction', formatNumber(violation.deduction, locale)],
        [
          'Score change',
          `${formatNumber(violation.score_before, locale)} → ${formatNumber(violation.score_after, locale)}`,
        ],
        ['Action', t(LABELS[violation.action] ?? violation.action)],
        [
          'Email notification',
          t(
            NOTIFICATION_LABELS[violation.notification_status] ??
              violation.notification_status
          ) || '—',
        ],
        [
          'Duplicate of',
          violation.duplicate_of
            ? `#${formatNumber(violation.duplicate_of, locale)}`
            : '—',
        ],
      ]
    : []
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !submitting) props.onClose()
      }}
      title={t('Violation details')}
      contentClassName='sm:max-w-3xl'
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {detail && violation && (
        <div className='min-w-0 space-y-4'>
          <div className='flex items-center gap-2'>
            <code className='min-w-0 text-sm break-all'>
              {violation.request_id}
            </code>
            <CopyButton value={violation.request_id} />
            <Badge
              variant={violation.status === 'pending' ? 'default' : 'secondary'}
            >
              {t(LABELS[violation.status] ?? violation.status)}
            </Badge>
          </div>
          <section
            aria-label={t('User account')}
            className='grid gap-2 rounded-lg border p-3 text-sm sm:grid-cols-2'
          >
            <p className='font-medium'>
              {t('Current score: {{score}} / 100', {
                score: formatNumber(detail.score, locale),
              })}
            </p>
            <p>
              {t('Account status')}:{' '}
              {t(USER_STATUS_LABELS[detail.user_status] ?? 'User not found')}
            </p>
          </section>
          {violation.mode === 'observe' && (
            <Alert>
              <AlertDescription>
                {t(
                  'Observation mode recorded this violation without rejecting the request or deducting points.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {violation.action === 'ban_suggested' &&
            detail.user_status === 1 && (
              <Alert>
                <AlertDescription>
                  {t(
                    'The score was already 0. Automatic banning is off, so the account is still enabled; ban it manually if needed.'
                  )}
                </AlertDescription>
              </Alert>
            )}
          <dl className='grid gap-3 text-sm sm:grid-cols-3'>
            {fields.map(([label, value]) => (
              <div key={label} className='min-w-0'>
                <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
                <dd className='break-all'>{value}</dd>
              </div>
            ))}
          </dl>
          {violation.rule && (
            <section className='space-y-1'>
              <h3 className='text-sm font-medium'>{t('Matched rule')}</h3>
              <pre className='bg-muted rounded-lg p-2 text-xs break-all whitespace-pre-wrap'>
                {violation.rule}
              </pre>
            </section>
          )}
          <section className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Decision model scores')}</h3>
            <StaticDataTable
              data={scores}
              getRowKey={(entry) => entry[0]}
              emptyContent={t('No data')}
              columns={[
                {
                  id: 'question',
                  header: t('Question or category'),
                  cell: (entry) => t(LABELS[entry[0]] ?? entry[0]),
                },
                {
                  id: 'score',
                  header: t('Safety score'),
                  cell: (entry) => formatNumber(entry[1], locale),
                },
              ]}
            />
          </section>
          <section className='space-y-2' aria-label={t('Request JSON')}>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <h3 className='text-sm font-medium'>{t('Request JSON')}</h3>
              <div className='flex items-center gap-2'>
                {body && <CopyButton value={body} />}
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  disabled={!body}
                  onClick={() => {
                    const blob = new Blob([body], {
                      type: 'application/json;charset=utf-8',
                    })
                    const url = URL.createObjectURL(blob)
                    const anchor = document.createElement('a')
                    anchor.href = url
                    anchor.download = `violation-${violation.id}.json`
                    anchor.click()
                    URL.revokeObjectURL(url)
                  }}
                >
                  {t('Download JSON')}
                </Button>
              </div>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t(
                'The complete request as received, with attachments replaced by placeholders. Size: {{bytes}} bytes.',
                { bytes: formatNumber(violation.request_body_bytes, locale) }
              )}
            </p>
            {violation.request_body_truncated && (
              <Alert>
                <AlertDescription>
                  {t(
                    'The saved request was truncated because it exceeded the storage limit.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {body ? (
              <pre className='bg-muted max-h-96 overflow-auto rounded-lg p-3 text-xs break-all whitespace-pre-wrap'>
                {body}
              </pre>
            ) : (
              <p className='text-muted-foreground text-sm'>
                {t('The request was not saved.')}
              </p>
            )}
          </section>
          {violation.reviewed_at > 0 && (
            <section className='space-y-1'>
              <h3 className='text-sm font-medium'>{t('Review note')}</h3>
              <p className='text-sm break-words whitespace-pre-wrap'>
                {violation.review_note}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t('Reviewer ID')}:{' '}
                {formatNumber(violation.reviewed_by, locale)} ·{' '}
                {new Date(violation.reviewed_at * 1000).toLocaleString(locale)}
              </p>
            </section>
          )}
          <div className='flex flex-wrap gap-2'>
            {(['uphold', 'revoke', 'ban', 'unban'] as const)
              .filter((type) => available[type])
              .map((type) => (
                <Button
                  key={type}
                  variant={
                    type === 'revoke' || type === 'ban' ? 'outline' : 'default'
                  }
                  disabled={submitting}
                  onClick={() => {
                    act.reset()
                    setAction(type)
                  }}
                >
                  {t(ACTION_LABELS[type])}
                </Button>
              ))}
          </div>
        </div>
      )}
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !submitting) setAction(null)
        }}
        title={t(action ? ACTION_LABELS[action] : 'Continue')}
        desc={action ? descriptions[action] : ''}
        confirmText={t(action ? ACTION_LABELS[action] : 'Continue')}
        isLoading={submitting}
        destructive={action === 'ban'}
        disabled={!noteValid || !action || !available[action]}
        handleConfirm={() => {
          if (!action || !violation || !noteValid || !available[action]) {
            return
          }
          act.mutate({ action, userId: violation.user_id, note: note.trim() })
        }}
      >
        <div className='space-y-3'>
          <Field
            label='Review note'
            hint='Required. At most 500 characters.'
          >
            <Textarea
              aria-label={t('Review note')}
              disabled={submitting}
              value={note}
              onChange={(event) => setNote(event.target.value)}
            />
          </Field>
          <ValidationErrors error={act.error} />
        </div>
      </ConfirmDialog>
    </Dialog>
  )
}
