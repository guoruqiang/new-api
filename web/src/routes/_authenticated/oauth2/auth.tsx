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
import { createFileRoute } from '@tanstack/react-router'
import axios from 'axios'
import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { api } from '@/lib/api'

export const Route = createFileRoute('/_authenticated/oauth2/auth')({
  component: OAuthAuthorization,
})

function OAuthAuthorization() {
  const { t } = useTranslation()
  const [errorMessage, setErrorMessage] = useState('')

  useEffect(() => {
    const controller = new AbortController()

    async function authorize() {
      try {
        const response = await api.post('/api/oauth2/auth', undefined, {
          params: Object.fromEntries(
            new URLSearchParams(window.location.search)
          ),
          disableDuplicate: true,
          skipErrorHandler: true,
          signal: controller.signal,
        })
        const redirectURI = response.data?.data?.redirect_uri
        if (typeof redirectURI !== 'string' || redirectURI.length === 0) {
          setErrorMessage(t('Request failed'))
          return
        }
        window.location.replace(redirectURI)
      } catch (error: unknown) {
        if (axios.isCancel(error)) return
        if (axios.isAxiosError(error)) {
          setErrorMessage(
            error.response?.data?.error_description ||
              error.response?.data?.message ||
              t('Request failed')
          )
          return
        }
        setErrorMessage(
          error instanceof Error ? error.message : t('Request failed')
        )
      }
    }

    void authorize()
    return () => controller.abort()
  }, [t])

  return (
    <main className='flex min-h-[50vh] items-center justify-center p-6'>
      {errorMessage ? (
        <p className='text-destructive text-center text-sm'>{errorMessage}</p>
      ) : (
        <div className='text-muted-foreground flex items-center gap-2 text-sm'>
          <Loader2 className='h-4 w-4 animate-spin' aria-hidden='true' />
          <span>{t('Loading...')}</span>
        </div>
      )}
    </main>
  )
}
