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
import { renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { getAffiliateCode } from '../api'
import { useAffiliate } from '../hooks/use-affiliate'
import { generateAffiliateLink } from '../lib/affiliate'

const configured = vi.hoisted(() => ({ console_address: '' }))
vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: configured }),
}))
vi.mock('../api', () => ({
  getAffiliateCode: vi.fn(),
  transferAffiliateQuota: vi.fn(),
}))

beforeEach(() => {
  configured.console_address = ''
  vi.mocked(getAffiliateCode).mockResolvedValue({
    success: true,
    message: '',
    data: 'invite-code',
  })
})

test('referral links use the configured console origin and encode the invitation code', () => {
  expect(
    generateAffiliateLink('invite+code', 'https://console.example.com/')
  ).toBe('https://console.example.com/sign-up?aff=invite%2Bcode')
})

test('an unset console address keeps referral links on the current website', () => {
  expect(generateAffiliateLink('invite-code', '')).toBe(
    `${window.location.origin}/sign-up?aff=invite-code`
  )
})

test('referral links update when the status address changes without fetching another invitation code', async () => {
  const { result, rerender } = renderHook(() => useAffiliate())
  await waitFor(() => expect(result.current.affiliateCode).toBe('invite-code'))
  expect(result.current.affiliateLink).toBe(
    `${window.location.origin}/sign-up?aff=invite-code`
  )
  configured.console_address = 'https://console.example.com'
  rerender()
  expect(result.current.affiliateLink).toBe(
    'https://console.example.com/sign-up?aff=invite-code'
  )
  configured.console_address = ''
  rerender()
  expect(result.current.affiliateLink).toBe(
    `${window.location.origin}/sign-up?aff=invite-code`
  )
  expect(getAffiliateCode).toHaveBeenCalledTimes(1)
})
