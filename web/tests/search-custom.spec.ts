import { expect, test } from '@playwright/test'

test.use({ timezoneId: 'Europe/Moscow' })

const user = { username: 'test-admin', role: 'Admin', enabled: true, created_at: '2026-10-02T00:00:00Z', updated_at: '2026-10-02T00:00:00Z' }

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-10-04T12:00:00+03:00'))
  await page.addInitScript(() => localStorage.setItem('pulse-language', 'en'))
  await page.route('**/api/v1/auth/session', (route) => route.fulfill({ json: { state: 'authenticated', csrf_token: 'csrf-test', user } }))
  await page.route('**/api/v1/meta', (route) => route.fulfill({ json: {
    name: 'Pulse', product: 'DNS Traffic Visibility', api: 'v1', ranges: ['15m', '1h', '3h', '6h', '12h', '24h'],
    retention: { raw: '24h' }, capabilities: { search: true, cursor_search: true },
    live: { transport: 'sse', default_rate: 200, maximum_rate: 1000, server_filtering: true }, time: '2026-10-04T09:00:00Z',
  } }))
})

test('custom search sends browser-local RFC3339 bounds and preserves them on page two', async ({ page }) => {
  const requests: URL[] = []
  await page.route('**/api/v1/search**', async (route) => {
    const url = new URL(route.request().url())
    requests.push(url)
    const custom = url.searchParams.get('range') === 'custom'
    await route.fulfill({ json: {
      range: custom ? 'custom' : url.searchParams.get('range'), count: 0, events: [],
      ...(custom && !url.searchParams.get('cursor') ? { next_cursor: 'page-two' } : {}),
    } })
  })

  await page.goto('/search')
  await expect(page.getByLabel('Range').locator('option')).toHaveText(['15m', '1h', '3h', '6h', '12h', '24h', 'Custom'])
  expect(requests[0].searchParams.get('range')).toBe('6h')
  expect(requests[0].searchParams.has('from')).toBeFalsy()

  await page.getByLabel('Range').selectOption('custom')
  await expect(page.getByLabel('From', { exact: true })).toBeVisible()
  await expect(page.getByLabel('To', { exact: true })).toBeVisible()
  await expect(page.getByLabel('From', { exact: true })).toHaveAttribute('step', '1')
  await expect(page.getByLabel('To', { exact: true })).toHaveAttribute('step', '1')
  await expect(page.getByText('Europe/Moscow (UTC+03:00)')).toBeVisible()
  await page.getByLabel('From', { exact: true }).fill('2026-10-04T10:27')
  await page.getByLabel('To', { exact: true }).fill('2026-10-04T10:29')
  await page.getByLabel('Source').fill('dns1')
  await page.getByLabel('RCODE').fill('SERVFAIL')
  await page.getByLabel('Outcome').selectOption('RESPONSE')
  await page.getByRole('button', { name: 'Run search' }).click()

  await expect.poll(() => requests.filter((url) => url.searchParams.get('range') === 'custom').length).toBe(1)
  const firstPage = requests.find((url) => url.searchParams.get('range') === 'custom')!
  expect(firstPage.searchParams.get('from')).toBe('2026-10-04T10:27:00+03:00')
  expect(firstPage.searchParams.get('to')).toBe('2026-10-04T10:29:00+03:00')
  expect(firstPage.searchParams.get('source')).toBe('dns1')
  expect(firstPage.searchParams.get('rcode')).toBe('SERVFAIL')
  expect(firstPage.searchParams.get('outcome')).toBe('RESPONSE')

  await page.getByRole('button', { name: 'Load next page' }).click()
  await expect.poll(() => requests.filter((url) => url.searchParams.get('cursor') === 'page-two').length).toBe(1)
  const pageTwo = requests.find((url) => url.searchParams.get('cursor') === 'page-two')!
  expect(pageTwo.searchParams.get('from')).toBe(firstPage.searchParams.get('from'))
  expect(pageTwo.searchParams.get('to')).toBe(firstPage.searchParams.get('to'))

  await page.getByLabel('Range').selectOption('1h')
  await expect(page.getByLabel('From', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Run search' }).click()
  await expect.poll(() => requests.filter((url) => url.searchParams.get('range') === '1h').length).toBe(1)
  const relative = requests.find((url) => url.searchParams.get('range') === '1h')!
  expect(relative.searchParams.has('from')).toBeFalsy()
  expect(relative.searchParams.has('to')).toBeFalsy()
})

test('custom search validates ordering and warns about expired retention', async ({ page }) => {
  let requests = 0
  await page.route('**/api/v1/search**', (route) => {
    requests += 1
    return route.fulfill({ json: { range: '6h', count: 0, events: [] } })
  })
  await page.goto('/search')
  await page.getByLabel('Range').selectOption('custom')
  await page.getByLabel('From', { exact: true }).fill('2026-10-04T10:29')
  await page.getByLabel('To', { exact: true }).fill('2026-10-04T10:29')
  const beforeInvalidSubmit = requests
  await page.getByRole('button', { name: 'Run search' }).click()
  await expect(page.getByRole('alert')).toHaveText('From must be earlier than To.')
  expect(requests).toBe(beforeInvalidSubmit)

  await page.getByLabel('From', { exact: true }).fill('2026-10-02T10:27')
  await page.getByLabel('To', { exact: true }).fill('2026-10-02T10:29')
  await expect(page.getByRole('status')).toContainText('outside raw event retention')
})
