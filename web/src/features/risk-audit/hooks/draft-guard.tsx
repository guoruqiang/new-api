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
import { useState, useCallback, useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'

import { DraftContext } from './use-risk-draft'

export function DraftGuard(props: { children: ReactNode }) {
  const { t } = useTranslation()
  const [drafts, setDrafts] = useState<Record<string, boolean>>({})
  const [pending, setPending] = useState<(() => void) | null>(null)
  const dirty = Object.values(drafts).some(Boolean)
  const report = useCallback(
    (id: string, value: boolean) =>
      setDrafts((old) => {
        if (!!old[id] === value) return old
        const next = { ...old }
        if (value) next[id] = true
        else delete next[id]
        return next
      }),
    []
  )
  const confirmDiscard = useCallback(
    (action: () => void) => {
      if (dirty) setPending(() => action)
      else action()
    },
    [dirty]
  )
  const context = useMemo(
    () => ({ report, confirmDiscard }),
    [report, confirmDiscard]
  )
  return (
    <DraftContext.Provider value={context}>
      <FormNavigationGuard when={dirty} />
      {props.children}
      <ConfirmDialog
        open={!!pending}
        onOpenChange={(open) => {
          if (!open) setPending(null)
        }}
        title={t('Unsaved changes')}
        desc={t('You have unsaved changes. Are you sure you want to leave?')}
        handleConfirm={() => {
          pending?.()
          setPending(null)
        }}
      />
    </DraftContext.Provider>
  )
}
