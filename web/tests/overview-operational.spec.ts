import { expect, test } from '@playwright/test'

test('overview exposes operational DNS metrics and real bucket semantics', async ({ page }) => {
  const now = new Date().toISOString()
  let sourcesMode: 'normal' | 'offline' = 'normal'
  const requestedSourceRanges = new Set<string>()
  const user = { username: 'test-admin', role: 'Admin', enabled: true, created_at: now, updated_at: now }
  await page.addInitScript(() => localStorage.setItem('pulse-language', 'en'))
  await page.route('**/api/v1/auth/session', (route) => route.fulfill({ json: { state: 'authenticated', csrf_token: 'csrf', user } }))
  await page.route('**/api/v1/overview?**', (route) => route.fulfill({ json: {
    range: '6h', current_qps: 417, queries: 9_007_200, responses: 8_980_000, no_response: 27_200, no_response_pct: 0.3,
    unmatched_responses: 0, queries_today: 20_000_000, unique_clients: 1200, unique_domains: 85_000,
    nxdomain: 238_500, nxdomain_pct: 2.65, servfail: 16_200, servfail_pct: 0.18,
    avg_latency_us: 4300, p50_latency_us: 900, p95_latency_us: 5300, p99_latency_us: 12000,
    response_bytes: 1_000_000, active_sources: 1,
    previous: { current_qps: 385, queries: 8_400_000, nxdomain_pct: 2.4, servfail_pct: 0.12, p95_latency_us: 4900 },
  } }))
  await page.route('**/api/v1/traffic?**', (route) => route.fulfill({ json: [
    { time: now, queries: 25020, responses: 24970, no_response: 50, unmatched_responses: 0, nxdomain: 662, servfail: 44, avg_latency_us: 1200, p50_latency_us: 800, p95_latency_us: 5300, latency_samples: 24970, qps: 417, bucket_seconds: 60, nxdomain_pct: 2.65, servfail_pct: 0.18 },
    { time: new Date(Date.now() + 60_000).toISOString(), queries: 25200, responses: 25100, no_response: 100, unmatched_responses: 0, nxdomain: 600, servfail: 50, avg_latency_us: 1300, p50_latency_us: 850, p95_latency_us: 5500, latency_samples: 25100, qps: 420, bucket_seconds: 60, nxdomain_pct: 2.39, servfail_pct: 0.2 },
  ] }))
  await page.route('**/api/v1/domains/top?**', (route) => route.fulfill({ json: [{ domain: 'example.test', queries: 1000, nxdomain: 10, servfail: 1 }] }))
  await page.route('**/api/v1/clients/top?**', (route) => route.fulfill({ json: [{ client_ip: '192.0.2.10', queries: 900, responses: 895, no_response: 5, nxdomain: 9, servfail: 1, nxdomain_pct: 1 }] }))
  await page.route('**/api/v1/rcodes?**', (route) => route.fulfill({ json: [{ rcode: 'NOERROR', queries: 800 }, { rcode: 'NXDOMAIN', queries: 100 }, { rcode: 'SERVFAIL', queries: 20 }, { rcode: 'REFUSED', queries: 5 }] }))
  await page.route('**/api/v1/sources?**', (route) => {
    requestedSourceRanges.add(new URL(route.request().url()).searchParams.get('range') ?? '')
    const dns2 = sourcesMode === 'normal'
      ? { source_id: 'dns2', queries: 2_007_200, responses: 2_000_000, nxdomain: 62_000, nxdomain_pct: 3.1, servfail: 3600, servfail_pct: 0.18, current_qps: 310, p95_latency_us: 47, last_seen: now, last_message_at: new Date(Date.now() - 47_000).toISOString(), last_persisted_at: new Date(Date.now() - 47_000).toISOString(), age_seconds: 47, ingest_lag_seconds: 47, expected: true, connected: false, state: 'silent', health: 'degraded' }
      : { source_id: 'dns2', queries: 0, responses: 0, nxdomain: 0, nxdomain_pct: 0, servfail: 0, servfail_pct: 0, current_qps: 0, p95_latency_us: 0, last_seen: null, last_message_at: null, last_persisted_at: null, age_seconds: null, ingest_lag_seconds: null, expected: true, connected: false, state: 'offline', health: 'offline' }
    return route.fulfill({ json: [
      { source_id: 'dns1', queries: 7_000_000, responses: 6_950_000, nxdomain: 194_600, nxdomain_pct: 2.8, servfail: 8340, servfail_pct: 0.12, current_qps: 1350, p95_latency_us: 42, last_seen: now, last_message_at: now, last_persisted_at: now, age_seconds: 0, ingest_lag_seconds: 0.2, expected: true, connected: true, state: 'active', health: 'healthy' },
      dns2,
    ] })
  })
  await page.route('**/api/v1/anomalies', (route) => route.fulfill({ json: { window: '15m', count: 0, anomalies: [] } }))

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'DNS overview' })).toBeVisible()
  await expect(page.getByLabel('Time range').getByRole('button')).toHaveText(['15m', '1h', '3h', '6h', '12h', '24h'])
  await expect(page.getByText('417', { exact: true }).first()).toBeVisible()
  await expect(page.locator('.metric-comparison')).toHaveCount(0)
  await expect(page.getByText(/vs previous/i)).toHaveCount(0)
  const comparison = page.locator('.resolver-comparison')
  const dns1 = comparison.locator('.resolver-comparison-source').filter({ hasText: 'dns1' })
  const dns2 = comparison.locator('.resolver-comparison-source').filter({ hasText: 'dns2' })
  await expect(page.getByRole('heading', { name: 'Resolver comparison' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Resolver health' })).toHaveCount(0)
  await expect(dns1).toContainText('healthy')
  await expect(dns1).toContainText('1,350')
  await expect(dns1).toContainText('2.80%')
  await expect(dns1).toContainText('0.12%')
  await expect(dns1).toContainText('42 µs')
  await expect(dns2).toContainText('degraded')
  await expect(dns2).toContainText('310')
  await expect(dns2).toContainText('3.10%')
  await expect(dns2).toContainText('0.18%')
  await expect(dns2).toContainText('47 µs')
  await expect(page.getByText(/dns2 last seen 47 s ago/)).toBeVisible()
  const shares = await comparison.locator('.resolver-share-legend strong').allTextContents()
  expect(shares).toEqual(['81.3%', '18.7%'])
  expect(shares.reduce((sum, value) => sum + Number.parseFloat(value), 0)).toBeCloseTo(100, 1)
  await expect(page.getByRole('heading', { name: 'RCODE distribution' })).toBeVisible()
  await expect(page.getByLabel('Chart metric').getByRole('button')).toHaveText(['Traffic', 'Errors', 'Latency'])
  await page.locator('.recharts-wrapper').hover({ position: { x: 300, y: 120 } })
  await expect(page.locator('.operational-tooltip')).toContainText('QPS avg')
  await expect(page.locator('.operational-tooltip')).toContainText('queries / 1m')
  await expect(page.locator('.operational-tooltip')).toContainText('NXDOMAIN')
  await expect(page.locator('.operational-tooltip')).toContainText('SERVFAIL')
  await expect(page.locator('.operational-tooltip')).toContainText('P95 latency')
  await page.getByLabel('Chart metric').getByRole('button', { name: 'Errors' }).click()
  await expect(page.getByLabel('Chart metric').getByRole('button', { name: 'Errors' })).toHaveClass(/selected/)
  await page.getByLabel('Chart metric').getByRole('button', { name: 'Latency' }).click()
  await expect(page.getByLabel('Chart metric').getByRole('button', { name: 'Latency' })).toHaveClass(/selected/)
  await expect(page.getByRole('link', { name: 'example.test' })).toHaveAttribute('href', '/search?range=6h&domain=example.test')
  await expect(page.getByRole('link', { name: '192.0.2.10' })).toHaveAttribute('href', '/search?range=6h&client_ip=192.0.2.10')

  sourcesMode = 'offline'
  await page.getByLabel('Time range').getByRole('button', { name: '1h' }).click()
  await expect(dns2).toContainText('offline')
  await expect(dns2.getByText('N/A')).toHaveCount(4)

  sourcesMode = 'normal'
  for (const selectedRange of ['15m', '1h', '3h', '6h', '12h', '24h']) {
    await page.getByLabel('Time range').getByRole('button', { name: selectedRange, exact: true }).click()
    await expect(page.getByLabel('Time range').getByRole('button', { name: selectedRange, exact: true })).toHaveClass(/selected/)
  }
  expect([...requestedSourceRanges]).toEqual(expect.arrayContaining(['15m', '1h', '3h', '6h', '12h', '24h']))

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(comparison).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
})
