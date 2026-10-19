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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  getServerErrorMessage,
  getServerErrorSources,
} from '@/lib/server-error-message'

export function ValidationErrors(props: { error: unknown }) {
  const { t } = useTranslation()
  for (const source of getServerErrorSources(props.error)) {
    const data = source.data
    if (
      !data ||
      typeof data !== 'object' ||
      !('problems' in data) ||
      !Array.isArray(data.problems) ||
      data.problems.length === 0
    ) {
      continue
    }
    return (
      <ul
        role='alert'
        className='text-destructive list-inside list-disc space-y-1 text-sm'
      >
        {data.problems.map((problem: unknown) => {
          if (
            !problem ||
            typeof problem !== 'object' ||
            !('key' in problem) ||
            typeof problem.key !== 'string'
          ) {
            return null
          }
          const params =
            'params' in problem &&
            typeof problem.params === 'object' &&
            problem.params
              ? problem.params
              : {}
          return (
            <li key={JSON.stringify(problem)}>
              {t(problem.key, params as Record<string, unknown>)}
            </li>
          )
        })}
      </ul>
    )
  }
  if (!props.error) return null
  return (
    <Alert variant='destructive'>
      <AlertDescription>{getServerErrorMessage(props.error)}</AlertDescription>
    </Alert>
  )
}
