import { expect, test, type Page } from '@playwright/test'

test.use({ timezoneId: 'Europe/Moscow' })

const bucketStart = '2026-10-04T07:28:00.000Z'
const user = { username: 'test-admin', role: 'Admin', enabled: true, created_at: bucketStart, updated_at: bucketStart }

async function mockOverview(page: Page) {
  const searchRequests: URL[] = []
  const trafficRanges: string[] = []

  await page.clock.setFixedTime(new Date('2026-10-04T12:00:00+03:00'))
  await page.addInitScript(() => localStorage.setItem('pulse-language', 'en'))
  await page.route('**/api/v1/auth/session', (route) => route.fulfill({ json: { state: 'authenticated', csrf_token: 'csrf-test', user } }))
  await page.route('**/api/v1/meta', (route) => route.fulfill({ json: {
    name: 'Pulse', product: 'DNS Traffic Visibility', api: 'v1', ranges: ['15m', '1h', '3h', '6h', '12h', '24h'],
    retention: { raw: '24h' }, capabilities: { search: true, cursor_search: true },
    live: { transport: 'sse', default_rate: 200, maximum_rate: 1000, server_filtering: true }, time: '2026-10-04T09:00:00Z',
  } }))
  await page.route('**/api/v1/overview?**', (route) => route.fulfill({ json: {
    range: new URL(route.request().url()).searchParams.get('range'), current_qps: 417, queries: 50_000, responses: 49_900,
    no_response: 100, no_response_pct: 0.2, unmatched_responses: 0, queries_today: 80_000, unique_clients: 20,
    unique_domains: 200, nxdomain: 1_325, nxdomain_pct: 2.65, servfail: 90, servfail_pct: 0.18,
    avg_latency_us: 1_200, p50_latency_us: 800, p95_latency_us: 5_300, p99_latency_us: 9_000,
    response_bytes: 1_000_000, active_sources: 1,
    previous: { current_qps: 400, queries: 48_000, nxdomain_pct: 2.5, servfail_pct: 0.2, p95_latency_us: 5_000 },
  } }))
  await page.route('**/api/v1/traffic?**', (route) => {
    const range = new URL(route.request().url()).searchParams.get('range') ?? '6h'
    trafficRanges.push(range)
    const bucketSeconds = range === '12h' || range === '24h' ? 300 : 60
    const next = new Date(Date.parse(bucketStart) + bucketSeconds * 1_000).toISOString()
    return route.fulfill({ json: [
      { time: bucketStart, queries: 25_020, responses: 24_970, no_response: 50, unmatched_responses: 0, nxdomain: 662, servfail: 44, avg_latency_us: 1_200, p50_latency_us: 800, p95_latency_us: 5_300, latency_samples: 24_970, qps: 417, bucket_seconds: bucketSeconds, nxdomain_pct: 2.65, servfail_pct: 0.18 },
      { time: next, queries: 25_200, responses: 25_100, no_response: 100, unmatched_responses: 0, nxdomain: 600, servfail: 50, avg_latency_us: 1_300, p50_latency_us: 850, p95_latency_us: 5_500, latency_samples: 25_100, qps: 420, bucket_seconds: bucketSeconds, nxdomain_pct: 2.39, servfail_pct: 0.2 },
    ] })
  })
  await page.route('**/api/v1/domains/top?**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/clients/top?**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/rcodes?**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/sources?**', (route) => route.fulfill({ json: [] }))
  await page.route('**/api/v1/anomalies', (route) => route.fulfill({ json: { window: '15m', count: 0, anomalies: [] } }))
  await page.route('**/api/v1/search**', (route) => {
    searchRequests.push(new URL(route.request().url()))
    return route.fulfill({ json: { range: 'custom', count: 0, events: [] } })
  })

  return { searchRequests, trafficRanges }
}

async function clickSeries(page: Page, series: string) {
  const chart = page.locator('.chart-clickable .recharts-wrapper')
  await expect(chart).toBeVisible()
  await chart.hover({ position: { x: 240, y: 100 } })
  const dot = page.locator(`circle[data-series="${series}"]`).first()
  await expect(dot).toBeVisible()
  await dot.click()
}

function expectExactBucket(url: URL, bucketSeconds: number) {
  expect(url.pathname).toBe('/search')
  expect(url.searchParams.get('range')).toBe('custom')
  const from = Date.parse(url.searchParams.get('from') ?? '')
  const to = Date.parse(url.searchParams.get('to') ?? '')
  expect(Number.isNaN(from)).toBeFalsy()
  expect(from).toBe(Date.parse(bucketStart))
  expect(to - from).toBe(bucketSeconds * 1_000)
}

test('SERVFAIL bucket opens and automatically runs exact custom search', async ({ page }) => {
  const { searchRequests } = await mockOverview(page)
  await page.goto('/')
  await page.getByLabel('Chart metric').getByRole('button', { name: 'Errors' }).click()

  await clickSeries(page, 'servfail_pct')

  await expect(page).toHaveURL(/\/search\?range=custom/)
  const navigation = new URL(page.url())
  expectExactBucket(navigation, 60)
  expect(navigation.searchParams.get('rcode')).toBe('SERVFAIL')
  await expect(page.getByLabel('Range')).toHaveValue('custom')
  await expect(page.getByLabel('RCODE')).toHaveValue('SERVFAIL')
  await expect.poll(() => searchRequests.length).toBe(1)
  expect(Date.parse(searchRequests[0].searchParams.get('from') ?? '')).toBe(Date.parse(navigation.searchParams.get('from') ?? ''))
  expect(Date.parse(searchRequests[0].searchParams.get('to') ?? '')).toBe(Date.parse(navigation.searchParams.get('to') ?? ''))
  expect(searchRequests[0].searchParams.get('rcode')).toBe('SERVFAIL')
})

test('NXDOMAIN bucket maps only the selected error series', async ({ page }) => {
  const { searchRequests } = await mockOverview(page)
  await page.goto('/')
  await page.getByLabel('Chart metric').getByRole('button', { name: 'Errors' }).click()

  await clickSeries(page, 'nxdomain_pct')

  const navigation = new URL(page.url())
  expectExactBucket(navigation, 60)
  expect(navigation.searchParams.get('rcode')).toBe('NXDOMAIN')
  expect(navigation.searchParams.has('source')).toBeFalsy()
  expect(navigation.searchParams.has('outcome')).toBeFalsy()
  await expect.poll(() => searchRequests.length).toBe(1)
  expect(searchRequests[0].searchParams.get('rcode')).toBe('NXDOMAIN')
})

test('QPS uses the real 24h bucket, keeps its instant in local time, and adds no error filter', async ({ page }) => {
  const { searchRequests, trafficRanges } = await mockOverview(page)
  await page.goto('/')
  await page.getByLabel('Time range').getByRole('button', { name: '24h', exact: true }).click()
  await expect.poll(() => trafficRanges.filter((range) => range === '24h').length).toBe(1)

  const chart = page.locator('.chart-clickable .recharts-wrapper')
  await chart.hover({ position: { x: 240, y: 100 } })
  await expect(page.locator('.operational-tooltip')).toContainText('Open events for this interval')
  await expect(page.locator('.operational-tooltip')).toContainText('queries / 5m')
  await clickSeries(page, 'qps')

  const navigation = new URL(page.url())
  expectExactBucket(navigation, 300)
  expect(navigation.searchParams.has('rcode')).toBeFalsy()
  expect(navigation.searchParams.has('outcome')).toBeFalsy()
  expect(navigation.searchParams.has('source')).toBeFalsy()
  await expect(page.getByLabel('From', { exact: true })).toHaveValue('2026-10-04T10:28')
  await expect(page.getByLabel('To', { exact: true })).toHaveValue('2026-10-04T10:33')
  await expect(page.getByText('Europe/Moscow (UTC+03:00)')).toBeVisible()
  await expect.poll(() => searchRequests.length).toBe(1)
  expect(Date.parse(searchRequests[0].searchParams.get('from') ?? '')).toBe(Date.parse(bucketStart))
  expect(Date.parse(searchRequests[0].searchParams.get('to') ?? '')).toBe(Date.parse(bucketStart) + 300_000)
})
