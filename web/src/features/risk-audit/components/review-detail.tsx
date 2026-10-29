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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import type { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'

import { riskGet, riskWrite } from '../api'
import {
  JUDGMENT_SOURCE_LABELS,
  LABELS,
  NOTIFICATION_ERROR_LABELS,
  NOTIFICATION_LABELS,
  SEVERITY_LABELS,
  SEVERITY_STATUS_LABELS,
} from '../constants'
import { reviewResolveSchema } from '../lib/schema'
import type { RiskReviewDetail } from '../types'
import { Choice, Field, NumberField } from './fields'
import { ValidationErrors } from './validation-errors'

type ReviewAction = 'confirm' | 'dismiss' | 'revoke' | 'ban'

export function ReviewDetail(props: {
  id: number
  canWrite: boolean
  canUnblock: boolean
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const client = useQueryClient()
  const [action, setAction] = useState<{
    type: ReviewAction
    revision: number
  } | null>(null)
  const form = useForm<z.infer<typeof reviewResolveSchema>>({
    resolver: zodResolver(reviewResolveSchema),
    defaultValues: { category: 'custom', note: '', ttl_minutes: 30 },
  })
  const values = form.watch()
  const query = useQuery({
    queryKey: ['risk', 'review', props.id],
    queryFn: () => riskGet<RiskReviewDetail>(`reviews/${props.id}`),
  })
  const resolve = useMutation({
    meta: { errorToast: false },
    mutationFn: (
      input: z.infer<typeof reviewResolveSchema> & {
        revision: number
        action: ReviewAction
      }
    ) =>
      riskWrite<RiskReviewDetail>('post', `reviews/${props.id}/resolve`, input),
    onSuccess: (detail) => {
      setAction(null)
      form.reset({ category: 'custom', note: '', ttl_minutes: 30 })
      client.setQueryData(['risk', 'review', props.id], detail)
      void client.invalidateQueries({ queryKey: ['risk', 'reviews'] })
      void client.invalidateQueries({ queryKey: ['risk', 'blocks'] })
      void client.invalidateQueries({ queryKey: ['risk', 'refusals'] })
    },
    onError: (error) => {
      if (getServerErrorStatus(error) === 409) void query.refetch()
    },
  })
  const detail = query.data
  const review = detail?.review
  const conflict = getServerErrorStatus(resolve.error) === 409
  const missingEvidence = !review?.evidence?.trim()
  const incompleteEvidence = missingEvidence || !!review?.evidence_truncated
  const incompleteOriginalRequest =
    !review?.original_request?.trim() || !!review?.original_request_truncated
  const missingToken = (review?.token_id ?? 0) <= 0
  const confirmUnavailable =
    incompleteEvidence ||
    incompleteOriginalRequest ||
    missingToken ||
    !detail?.behavior_enabled
  const banUnavailable =
    incompleteEvidence ||
    incompleteOriginalRequest ||
    missingToken ||
    review?.mode !== 'enforce'
  const submitting = resolve.isPending
  const block = detail?.block
  const blockActive =
    !!block &&
    !block.released_at &&
    (!block.expires_at || block.expires_at * 1000 > Date.now())
  const confirmedBan = action?.type === 'ban' && review?.status === 'confirmed'
  const labels = {
    confirm: 'Confirm violation',
    dismiss: 'Dismiss candidate',
    revoke: 'Revoke confirmation',
    ban: 'Ban this API token',
  }
  let actionDescription =
    'Check the saved evidence and category before recording this review. Observation confirmations do not change reputation.'
  if (action?.type === 'revoke') {
    actionDescription =
      'Revoking removes this deduction, recalculates reputation, clears related refusal records, and releases an automatic block that depended on this incident.'
  }
  if (action?.type === 'ban') {
    actionDescription =
      'Ban only the API token linked to this incident, across all groups. Use 0 minutes for a permanent manual ban; other tokens and the account remain available.'
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !submitting) props.onClose()
      }}
      title={t('Risk review details')}
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {detail && review && (
        <div className='space-y-4'>
          <div className='flex items-center gap-2'>
            <p className='min-w-0 text-sm break-all'>{review.request_id}</p>
            <CopyButton value={review.request_id} />
          </div>
          <dl className='grid gap-3 text-sm sm:grid-cols-2'>
            <div>
              <dt className='text-muted-foreground'>{t('Group namespace')}</dt>
              <dd className='break-all'>{review.namespace}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('User ID')}</dt>
              <dd>{formatNumber(review.user_id, locale)}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Token ID')}</dt>
              <dd>
                {missingToken ? '—' : formatNumber(review.token_id, locale)}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Judgment source')}</dt>
              <dd>
                {t(
                  JUDGMENT_SOURCE_LABELS[review.judgment_source] ??
                    review.judgment_source
                ) || '—'}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Model review trigger')}
              </dt>
              <dd>
                {t(
                  LABELS[review.screening_reason] ?? review.screening_reason
                ) || '—'}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Configuration revision')}
              </dt>
              <dd>{formatNumber(review.config_revision ?? 0, locale)}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Email notification')}
              </dt>
              <dd>
                {t(
                  NOTIFICATION_LABELS[review.notification_status] ??
                    review.notification_status
                ) || '—'}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Review status')}</dt>
              <dd>
                <Badge variant='secondary'>{t(LABELS[review.status])}</Badge>
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Mode')}</dt>
              <dd>{t(LABELS[review.mode])}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Category')}</dt>
              <dd>{t(LABELS[review.category] ?? review.category) || '—'}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>
                {t('Actual reputation deduction')}
              </dt>
              <dd aria-label={t('Actual reputation deduction')}>
                {formatNumber(review.deduction, locale)}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Reason')}</dt>
              <dd>{t(LABELS[review.reason] ?? review.reason)}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Decision model')}</dt>
              <dd>{review.provider_model || '—'}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Coverage')}</dt>
              <dd>{t(LABELS[review.coverage] ?? review.coverage) || '—'}</dd>
            </div>
            {review.severity && (
              <div>
                <dt className='text-muted-foreground'>{t('Severity')}</dt>
                <dd>
                  {t(SEVERITY_LABELS[review.severity] ?? review.severity)}
                </dd>
              </div>
            )}
            {review.severity_status && (
              <div>
                <dt className='text-muted-foreground'>
                  {t('Severity assessment')}
                </dt>
                <dd>
                  {t(
                    SEVERITY_STATUS_LABELS[review.severity_status] ??
                      review.severity_status
                  )}
                </dd>
              </div>
            )}
          </dl>
          {review.notification_error && (
            <p className='text-destructive text-sm'>
              {t(
                NOTIFICATION_ERROR_LABELS[review.notification_error] ??
                  review.notification_error
              )}
            </p>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Confirmed violations notify the bound email address; accounts without an email address are skipped.'
            )}
          </p>
          <section
            className='space-y-2 rounded-lg border p-3'
            aria-label={t('User reputation')}
          >
            <p className='font-medium'>
              {t('User reputation: {{score}} / {{initial}}', {
                score: formatNumber(detail.reputation.score, locale),
                initial: formatNumber(detail.reputation.initial_score, locale),
              })}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Automatic suspension threshold')}:{' '}
              {formatNumber(detail.reputation.ban_threshold, locale)} ·{' '}
              {t('Confirmed events')}:{' '}
              {formatNumber(detail.reputation.confirmed_events, locale)}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Total deducted')}:{' '}
              {formatNumber(detail.reputation.total_deducted, locale)} ·{' '}
              {t('Recovered points')}:{' '}
              {formatNumber(detail.reputation.recovered_points, locale)}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Last confirmed violation')}:{' '}
              {detail.reputation.last_violation_at
                ? new Date(
                    detail.reputation.last_violation_at * 1000
                  ).toLocaleString(locale)
                : '—'}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Next reputation recovery')}:{' '}
              {detail.reputation.next_recovery_at
                ? new Date(
                    detail.reputation.next_recovery_at * 1000
                  ).toLocaleString(locale)
                : '—'}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Reputation is shared by this user across API tokens. Automatic suspension only affects the token involved in the new violation.'
              )}
            </p>
            <p className='text-sm'>
              {t(
                detail.behavior_enabled &&
                  detail.auto_block_enabled &&
                  detail.mode === 'enforce'
                  ? 'Automatic blocking enabled'
                  : 'Record only'
              )}
            </p>
            <p className='text-sm'>
              {t(
                detail.auto_confirm_enabled
                  ? 'High-confidence model confirmation enabled'
                  : 'Manual confirmation only'
              )}
            </p>
            <p className='text-sm'>
              {t(
                blockActive ? 'Active subject block' : 'No active subject block'
              )}
            </p>
            {blockActive && (
              <p className='text-muted-foreground text-sm'>
                {t('Expires at')}:{' '}
                {block?.expires_at
                  ? new Date(block.expires_at * 1000).toLocaleString(locale)
                  : t('Permanent')}
              </p>
            )}
          </section>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Token bans apply across groups and do not affect other tokens or the account. The displayed group identifies the source request.'
            )}
          </p>
          {!detail.behavior_enabled && (
            <Alert>
              <AlertDescription>
                {t('Behavior scoring is disabled for this group')}
              </AlertDescription>
            </Alert>
          )}
          {review.mode === 'observe' && (
            <Alert>
              <AlertDescription>
                {t(
                  'This candidate came from observation mode. Confirmation records the review without deducting reputation or automatically suspending a token.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {!missingToken && incompleteOriginalRequest && (
            <Alert>
              <AlertDescription>
                {t(
                  'Complete request evidence is required to confirm this violation'
                )}
              </AlertDescription>
            </Alert>
          )}
          {missingToken && (
            <Alert>
              <AlertDescription>
                {t(
                  'Reviews without an API token cannot be confirmed or used to restrict tokens. Existing confirmations can still be revoked.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <section
            className='space-y-2'
            aria-label={t('Submitted review evidence')}
          >
            <h3 className='font-medium'>{t('Submitted review evidence')}</h3>
            {missingEvidence ? (
              <Alert>
                <AlertDescription>
                  {t(
                    'Review evidence is unavailable or was not stored. A short preview alone cannot confirm a violation.'
                  )}
                </AlertDescription>
              </Alert>
            ) : (
              <pre className='bg-muted max-h-80 overflow-auto rounded-lg p-3 text-sm break-words whitespace-pre-wrap'>
                {review.evidence}
              </pre>
            )}
            {review.evidence_truncated && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Stored review evidence is truncated; this incident cannot be confirmed'
                )}
              </p>
            )}
          </section>
          <section
            className='space-y-2'
            aria-label={t('Original role content')}
          >
            <div className='flex items-center justify-between gap-2'>
              <h3 className='font-medium'>{t('Original role content')}</h3>
              {review.original_request && (
                <CopyButton value={review.original_request} />
              )}
            </div>
            <p className='text-muted-foreground text-sm'>
              {t(
                'This is retained role content, not the full HTTP request or the audit window. Normal request bodies are not stored.'
              )}
            </p>
            {review.original_request ? (
              <pre className='bg-muted max-h-80 overflow-auto rounded-lg p-3 text-sm break-words whitespace-pre-wrap'>
                {review.original_request}
              </pre>
            ) : (
              <p className='text-muted-foreground text-sm'>
                {t('Original request evidence was not stored.')}
              </p>
            )}
            {review.original_request_truncated && (
              <p className='text-destructive text-sm'>
                {t(
                  'Original request evidence is truncated; token banning is unavailable.'
                )}
              </p>
            )}
            {review.original_request_hash && (
              <p className='text-muted-foreground text-xs break-all'>
                {t('Evidence hash')}: {review.original_request_hash}
              </p>
            )}
          </section>
          {review.assessments && (
            <section
              className='space-y-2'
              aria-label={t('Model category assessments')}
            >
              <h3 className='font-medium'>{t('Model category assessments')}</h3>
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Saved model assessments are shown as recorded. Historical severity or deduction values may differ from the actual reputation deduction above.'
                )}
              </p>
              <pre className='bg-muted max-h-80 overflow-auto rounded-lg p-3 text-xs break-words whitespace-pre-wrap'>
                {review.assessments}
              </pre>
            </section>
          )}
          {review.scores && (
            <section className='space-y-2'>
              <h3 className='font-medium'>{t('Question scores')}</h3>
              <pre className='bg-muted max-h-40 overflow-auto rounded-lg p-3 text-xs break-words whitespace-pre-wrap'>
                {review.scores}
              </pre>
            </section>
          )}
          {review.review_note && (
            <section className='space-y-2'>
              <h3 className='font-medium'>{t('Review note')}</h3>
              <p className='text-sm break-words whitespace-pre-wrap'>
                {review.review_note}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  JUDGMENT_SOURCE_LABELS[review.judgment_source] ??
                    'Administrator confirmation'
                )}
                {review.reviewed_by > 0 && (
                  <>
                    {' '}
                    · {t('Reviewer ID')}:{' '}
                    {formatNumber(review.reviewed_by, locale)}
                  </>
                )}{' '}
                · {new Date(review.reviewed_at * 1000).toLocaleString(locale)}
              </p>
            </section>
          )}
          {props.canWrite && (
            <div className='flex flex-wrap gap-2'>
              {(['confirm', 'dismiss', 'revoke', 'ban'] as const).map(
                (type) => {
                  if (
                    (type === 'revoke' || type === 'ban') &&
                    !props.canUnblock
                  ) {
                    return null
                  }
                  if (
                    type === 'ban' &&
                    !['pending', 'confirmed'].includes(review.status)
                  ) {
                    return null
                  }
                  if (
                    type !== 'ban' &&
                    (type === 'revoke'
                      ? review.status !== 'confirmed'
                      : review.status !== 'pending')
                  ) {
                    return null
                  }
                  return (
                    <Button
                      key={type}
                      variant={type === 'confirm' ? 'default' : 'outline'}
                      disabled={
                        submitting ||
                        (type === 'ban' && banUnavailable) ||
                        (type === 'confirm' && confirmUnavailable)
                      }
                      onClick={() => {
                        resolve.reset()
                        form.setValue(
                          'category',
                          reviewResolveSchema.shape.category.safeParse(
                            review.category
                          ).data ?? 'custom'
                        )
                        form.setValue(
                          'ttl_minutes',
                          type === 'ban'
                            ? (values.ttl_minutes ?? 30)
                            : undefined
                        )
                        setAction({ type, revision: review.revision })
                      }}
                    >
                      {t(labels[type])}
                    </Button>
                  )
                }
              )}
            </div>
          )}
          {props.canWrite && props.canUnblock && banUnavailable && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Token banning requires an enforced request, a token ID, and complete saved audit and original request evidence.'
              )}
            </p>
          )}
        </div>
      )}
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !submitting) setAction(null)
        }}
        title={t(action ? labels[action.type] : 'Review candidate')}
        desc={t(actionDescription)}
        confirmText={t(action ? labels[action.type] : 'Continue')}
        isLoading={submitting}
        destructive={action?.type === 'revoke' || action?.type === 'ban'}
        disabled={
          !props.canWrite ||
          conflict ||
          !values.note.trim() ||
          ((action?.type === 'revoke' || action?.type === 'ban') &&
            !props.canUnblock) ||
          (action?.type === 'ban' && banUnavailable) ||
          (action?.type === 'confirm' && confirmUnavailable)
        }
        handleConfirm={() =>
          void form.handleSubmit((value) => {
            if (
              !action ||
              conflict ||
              !props.canWrite ||
              !value.note.trim() ||
              ((action.type === 'revoke' || action.type === 'ban') &&
                !props.canUnblock) ||
              (action.type === 'ban' && banUnavailable) ||
              (action.type === 'confirm' && confirmUnavailable)
            ) {
              return
            }
            const { ttl_minutes, ...reviewFields } = value
            resolve.mutate({
              ...reviewFields,
              ...(action.type === 'ban' ? { ttl_minutes } : {}),
              action: action.type,
              revision: action.revision,
            })
          })()
        }
      >
        <div className='space-y-3'>
          {(action?.type === 'confirm' || action?.type === 'ban') && (
            <Choice
              label='Category'
              value={values.category}
              disabled={submitting || confirmedBan}
              options={reviewResolveSchema.shape.category.options.map(
                (category) => ({
                  value: category,
                  label: LABELS[category] ?? category,
                })
              )}
              onChange={(category) => {
                form.setValue(
                  'category',
                  reviewResolveSchema.shape.category.parse(category)
                )
              }}
            />
          )}
          {action?.type === 'ban' && (
            <>
              <p className='text-sm'>
                {t('Token ID')}: {formatNumber(review?.token_id ?? 0, locale)} ·{' '}
                {t('Group namespace')}: {review?.namespace}
              </p>
              <NumberField
                label='Token ban duration (minutes, 0 = permanent)'
                disabled={submitting}
                value={values.ttl_minutes ?? 30}
                min={0}
                max={1440}
                onChange={(ttl_minutes) =>
                  form.setValue('ttl_minutes', ttl_minutes)
                }
              />
              {form.formState.errors.ttl_minutes && (
                <p role='alert' className='text-destructive text-sm'>
                  {t(
                    'Token ban duration must be an integer from 0 to 1440 minutes.'
                  )}
                </p>
              )}
            </>
          )}
          {action?.type === 'confirm' && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Each distinct confirmed violation deducts 20 reputation points. Observation confirmations do not change reputation.'
              )}
            </p>
          )}
          <Field label='Review note' hint='Required'>
            <Textarea
              aria-label={t('Review note')}
              disabled={submitting}
              {...form.register('note')}
            />
          </Field>
          {form.formState.errors.note && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Review notes must not exceed 500 characters.')}
            </p>
          )}
          <ValidationErrors error={resolve.error} />
          {conflict && (
            <Alert>
              <AlertDescription>
                {t(
                  'This review changed. Close this confirmation and check the latest record before trying again. Your note is kept.'
                )}
              </AlertDescription>
            </Alert>
          )}
        </div>
      </ConfirmDialog>
    </Dialog>
  )
}
