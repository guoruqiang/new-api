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
import { LABELS } from '../constants'
import { reviewResolveSchema } from '../lib/schema'
import type { RiskReviewDetail } from '../types'
import { Choice, Field } from './fields'
import { ValidationErrors } from './validation-errors'

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
    type: 'confirm' | 'dismiss' | 'revoke'
    revision: number
  } | null>(null)
  const form = useForm<z.infer<typeof reviewResolveSchema>>({
    resolver: zodResolver(reviewResolveSchema),
    defaultValues: { category: 'custom', note: '' },
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
        action: 'confirm' | 'dismiss' | 'revoke'
      }
    ) =>
      riskWrite<RiskReviewDetail>('post', `reviews/${props.id}/resolve`, input),
    onSuccess: (detail) => {
      setAction(null)
      form.reset({ category: 'custom', note: '' })
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
  const submitting = resolve.isPending
  const block = detail?.block
  const blockActive =
    !!block &&
    !block.released_at &&
    (!block.expires_at || block.expires_at * 1000 > Date.now())
  const categoryPoints = ['nsfw', 'jailbreak'].includes(values.category) ? 3 : 1
  const confirmationPoints = review?.mode === 'observe' ? 0 : categoryPoints
  const labels = {
    confirm: 'Confirm violation',
    dismiss: 'Dismiss candidate',
    revoke: 'Revoke confirmation',
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
              <dt className='text-muted-foreground'>{t('Confirmed points')}</dt>
              <dd>{formatNumber(review.points, locale)}</dd>
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
          </dl>
          <section
            className='space-y-2 rounded-lg border p-3'
            aria-label={t('Behavior scoring')}
          >
            <p className='font-medium'>
              {t('Confirmed score: {{score}} / {{threshold}}', {
                score: formatNumber(detail.score.score, locale),
                threshold: formatNumber(detail.score.threshold, locale),
              })}
            </p>
            <p className='text-muted-foreground text-sm'>
              {t('Score window: {{minutes}} minutes', {
                minutes: formatNumber(detail.score.window_minutes, locale),
              })}{' '}
              · {t('Confirmed events')}:{' '}
              {formatNumber(detail.score.confirmed_events, locale)}
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
                  'This candidate came from observation mode. Confirming it records the review only: 0 points and no automatic block.'
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
                {t('Reviewer ID')}: {formatNumber(review.reviewed_by, locale)} ·{' '}
                {new Date(review.reviewed_at * 1000).toLocaleString(locale)}
              </p>
            </section>
          )}
          {props.canWrite && (
            <div className='flex flex-wrap gap-2'>
              {(['confirm', 'dismiss', 'revoke'] as const).map((type) => {
                if (type === 'revoke' && !props.canUnblock) return null
                if (
                  type === 'revoke'
                    ? review.status !== 'confirmed'
                    : review.status !== 'pending'
                ) {
                  return null
                }
                return (
                  <Button
                    key={type}
                    variant={type === 'confirm' ? 'default' : 'outline'}
                    disabled={
                      submitting ||
                      (type === 'confirm' &&
                        (incompleteEvidence || !detail.behavior_enabled))
                    }
                    onClick={() => {
                      resolve.reset()
                      form.setValue(
                        'category',
                        reviewResolveSchema.shape.category.safeParse(
                          review.category
                        ).data ?? 'custom'
                      )
                      setAction({ type, revision: review.revision })
                    }}
                  >
                    {t(labels[type])}
                  </Button>
                )
              })}
            </div>
          )}
        </div>
      )}
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !submitting) setAction(null)
        }}
        title={t(action ? labels[action.type] : 'Review candidate')}
        desc={t(
          action?.type === 'revoke'
            ? 'Revoking removes these points, clears related refusal records, and releases an automatic block that depended on this incident.'
            : 'Check the saved evidence and category before recording this review. Observation candidates do not add points.'
        )}
        confirmText={t(action ? labels[action.type] : 'Continue')}
        isLoading={submitting}
        destructive={action?.type === 'revoke'}
        disabled={
          !props.canWrite ||
          conflict ||
          !values.note.trim() ||
          (action?.type === 'revoke' && !props.canUnblock) ||
          (action?.type === 'confirm' &&
            (incompleteEvidence || !detail?.behavior_enabled))
        }
        handleConfirm={() =>
          void form.handleSubmit((value) => {
            if (
              !action ||
              conflict ||
              !props.canWrite ||
              !value.note.trim() ||
              (action.type === 'revoke' && !props.canUnblock) ||
              (action.type === 'confirm' &&
                (incompleteEvidence || !detail?.behavior_enabled))
            ) {
              return
            }
            resolve.mutate({
              ...value,
              action: action.type,
              revision: action.revision,
            })
          })()
        }
      >
        <div className='space-y-3'>
          {action?.type === 'confirm' && (
            <Choice
              label='Category'
              value={values.category}
              disabled={submitting}
              options={reviewResolveSchema.shape.category.options.map(
                (category) => ({
                  value: category,
                  label: LABELS[category] ?? category,
                })
              )}
              onChange={(category) =>
                form.setValue(
                  'category',
                  reviewResolveSchema.shape.category.parse(category)
                )
              }
            />
          )}
          {action?.type === 'confirm' && (
            <p className='text-muted-foreground text-sm'>
              {t('Points on confirmation: {{points}}', {
                points: formatNumber(confirmationPoints, locale),
              })}
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
