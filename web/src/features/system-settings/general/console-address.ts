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
export function isValidConsoleAddress(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return true
  // Reject inputs that URL would silently normalize into a different origin.
  if (!/^https?:\/\/[^/?#@\\\s]+\/?$/i.test(trimmed)) return false
  try {
    const url = new URL(trimmed)
    const authority = trimmed
      .slice(trimmed.indexOf('://') + 3)
      .replace(/\/$/, '')
    if (authority.endsWith(':') || (url.port !== '' && Number(url.port) < 1)) {
      return false
    }
    const rawHost = authority.startsWith('[')
      ? authority.slice(0, authority.indexOf(']') + 1)
      : authority.split(':')[0]
    const ipv4Loopback =
      /^127\.(?:\d{1,3}\.){2}\d{1,3}$/.test(rawHost) &&
      rawHost
        .split('.')
        .every((part) => /^(0|[1-9]\d{0,2})$/.test(part) && Number(part) <= 255)
    const loopback =
      rawHost.toLowerCase() === 'localhost' ||
      (rawHost.startsWith('[') && url.hostname === '[::1]') ||
      ipv4Loopback
    return (
      (url.protocol === 'https:' || (url.protocol === 'http:' && loopback)) &&
      url.hostname.length > 0 &&
      url.pathname === '/'
    )
  } catch {
    return false
  }
}
