import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../api'
import {
  EmptyState,
  ErrorState,
  LoadingState,
  MetricCard,
  PageHeader,
  Panel,
  RangePicker,
  StatusBadge,
  ViewAll,
} from '../components/Common'
import { TrafficChart, type TrafficChartMode } from '../components/TrafficChart'
import { formatCompact, formatDateTime, formatLatency, formatPercent, relativeTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { RCodeRow, SourceRow, TimeRange, TrafficPoint } from '../types'
import { useI18n } from '../i18n'

function formatAge(seconds: number | null, language: string): string {
  if (seconds === null || !Number.isFinite(seconds)) return '—'
  if (seconds < 1) return `${Math.max(0, Math.round(seconds * 1000))} ${language === 'ru' ? 'мс' : 'ms'}`
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)} ${language === 'ru' ? 'с' : 's'}`
  const minutes = Math.floor(seconds / 60)
  return language === 'ru' ? `${minutes} мин` : `${minutes} min`
}

function groupedRCodes(rows: RCodeRow[]) {
  const groups = new Map<string, number>([['NOERROR', 0], ['NXDOMAIN', 0], ['SERVFAIL', 0], ['OTHER', 0]])
  rows.forEach((row) => {
    const key = ['NOERROR', 'NXDOMAIN', 'SERVFAIL'].includes(row.rcode) ? row.rcode : 'OTHER'
    groups.set(key, (groups.get(key) ?? 0) + row.queries)
  })
  return [...groups.entries()].map(([rcode, queries]) => ({ rcode, queries }))
}

function sourceRate(value: number | undefined, responses: number): string {
  return responses > 0 && Number.isFinite(value) ? formatPercent(value as number) : 'N/A'
}

function sourceLatency(value: number | undefined, responses: number): string {
  return responses > 0 && Number.isFinite(value) ? formatLatency(value as number) : 'N/A'
}

function sourceQPS(value: number | undefined): number {
  return Number.isFinite(value) ? Math.max(0, value as number) : 0
}

function freshness(sources: SourceRow[]) {
  const expected = sources.filter((source) => source.expected)
  const monitored = expected.length ? expected : sources
  const unhealthy = monitored.find((source) => source.health !== 'healthy')
  const newest = monitored.reduce<SourceRow | null>((latest, source) => {
    if (!source.last_message_at) return latest
    if (!latest?.last_message_at || new Date(source.last_message_at) > new Date(latest.last_message_at)) return source
    return latest
  }, null)
  const worstLag = monitored.reduce((maximum, source) => Math.max(maximum, source.ingest_lag_seconds ?? 0), 0)
  const newestAge = newest?.last_message_at ? Math.max(0, (Date.now() - new Date(newest.last_message_at).getTime()) / 1000) : null
  const stale = monitored.reduce<SourceRow | null>((worst, source) => {
    if (source.age_seconds === null) return worst ?? source
    if (!worst || worst.age_seconds === null || source.age_seconds > worst.age_seconds) return source
    return worst
  }, null)
  const lagging = monitored.find((source) => (source.ingest_lag_seconds ?? 0) > 10)
  const freshnessProblem = stale?.age_seconds === null || (stale?.age_seconds ?? 0) > 10 ? stale : lagging
  const degraded = unhealthy ?? freshnessProblem
  const live = monitored.length > 0 && !degraded && newestAge !== null && worstLag <= 10
  return { status: live ? 'live' : 'degraded', degraded, newestAge, worstLag }
}

export function OverviewPage() {
  const { language, t } = useI18n()
  const navigate = useNavigate()
  const [range, setRange] = useState<TimeRange>('6h')
  const [chartMode, setChartMode] = useState<TrafficChartMode>('traffic')
  const state = useAsync(
    (signal) => Promise.all([
      api.overview(range, signal),
      api.traffic(range, signal),
      api.topDomains(range, 8, signal),
      api.topClients(range, 8, signal),
      api.rcodes(range, signal),
      api.sources(range, signal),
      api.anomalies(signal),
    ]).then(([overview, traffic, domains, clients, rcodes, sources, anomalies]) => ({
      overview, traffic, domains, clients, rcodes, sources, anomalies,
    })),
    [range],
    30_000,
  )

  const data = state.data
  const fresh = data ? freshness(data.sources) : null
  const responseCodes = data ? groupedRCodes(data.rcodes) : []
  const responseTotal = responseCodes.reduce((sum, row) => sum + row.queries, 0)
  const totalResolverQPS = data?.sources.reduce((sum, source) => sum + sourceQPS(source.current_qps), 0) ?? 0
  const healthySources = data?.sources.filter((source) => source.health === 'healthy').length ?? 0
  const freshnessDetail = fresh?.degraded
    ? `${fresh.degraded.source_id} ${t('last seen')} ${formatAge(fresh.degraded.age_seconds, language)} ${t('ago')}`
    : `${t('Last event')}: ${formatAge(fresh?.newestAge ?? null, language)} · ${t('Ingest lag')}: ${formatAge(fresh?.worstLag ?? null, language)}`
  const openBucket = (point: TrafficPoint, series: string) => {
    const start = new Date(point.time)
    if (Number.isNaN(start.getTime()) || !Number.isFinite(point.bucket_seconds) || point.bucket_seconds <= 0) return
    const end = new Date(start.getTime() + point.bucket_seconds * 1000)
    const params = new URLSearchParams({ range: 'custom', from: start.toISOString(), to: end.toISOString() })
    if (series === 'servfail_pct') params.set('rcode', 'SERVFAIL')
    if (series === 'nxdomain_pct') params.set('rcode', 'NXDOMAIN')
    navigate(`/search?${params.toString()}`)
  }

  return (
    <div className="page overview-page">
      <PageHeader
        eyebrow={t('OPERATIONS')}
        title={t('DNS overview')}
        description={t('Traffic, reliability, and resolver activity across the selected window.')}
        actions={<RangePicker value={range} onChange={setRange} />}
      />

      {state.error && !data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
      {state.loading && !data && <LoadingState rows={7} />}

      {data && fresh && (
        <>
          {state.error && <div className="inline-warning">{t('Refresh failed — showing the most recent successful response.')}</div>}

          <section className="metric-grid operational-metric-grid">
            <MetricCard label={t('Current QPS')} value={formatCompact(data.overview.current_qps)} />
            <MetricCard label={t('Queries in range')} value={formatCompact(data.overview.queries)} />
            <MetricCard label="NXDOMAIN" value={formatPercent(data.overview.nxdomain_pct)} tone={data.overview.nxdomain_pct >= 20 ? 'warning' : 'default'} />
            <MetricCard label="SERVFAIL" value={formatPercent(data.overview.servfail_pct)} tone={data.overview.servfail_pct >= 2 ? 'danger' : 'default'} />
            <MetricCard label={t('P95 latency')} value={formatLatency(data.overview.p95_latency_us)} />
            <MetricCard label={t('Resolvers')} value={`${healthySources}/${data.sources.length}`} detail={t('{count} healthy', { count: healthySources })} tone={healthySources === data.sources.length ? 'default' : 'warning'} />
            <MetricCard label={t('Data freshness')} value={<StatusBadge value={fresh.status} />} detail={freshnessDetail} tone={fresh.status === 'live' ? 'default' : 'warning'} />
          </section>

          <section className="secondary-metrics">
            <span><strong>{formatCompact(data.overview.queries_today)}</strong> {t('queries today')}</span>
            <span><strong>{formatCompact(data.overview.unique_clients)}</strong> {t('unique clients')}</span>
            <span><strong>{formatCompact(data.overview.unique_domains)}</strong> {t('unique domains')}</span>
          </section>

          <Panel
            title={t('DNS traffic')}
            subtitle={t('{range} window · {count} total queries', { range, count: formatCompact(data.overview.queries) })}
            className="traffic-panel"
            action={<div className="chart-mode-picker" aria-label={t('Chart metric')}>
              {(['traffic', 'errors', 'latency'] as TrafficChartMode[]).map((mode) => <button key={mode} type="button" className={chartMode === mode ? 'selected' : ''} onClick={() => setChartMode(mode)}>{t(mode[0].toUpperCase() + mode.slice(1))}</button>)}
            </div>}
          >
            <TrafficChart data={data.traffic} mode={chartMode} operational onBucketClick={openBucket} />
          </Panel>

          <Panel title={t('Resolver comparison')} subtitle={t('{range} metrics with realtime QPS', { range })} action={<ViewAll to="/sources" />} className="resolver-comparison-panel">
            {data.sources.length === 0 ? <EmptyState /> : <div className="resolver-comparison">
              <div className="resolver-comparison-grid">
                {data.sources.map((source) => {
                  const lastEvent = source.last_message_at ?? source.last_seen
                  return <Link className="resolver-comparison-source" to={`/sources/${encodeURIComponent(source.source_id)}`} key={source.source_id}>
                    <header><strong>{source.source_id}</strong><StatusBadge value={source.health} /></header>
                    <div className="resolver-qps"><strong>{Number.isFinite(source.current_qps) ? formatCompact(source.current_qps) : 'N/A'}</strong><span>QPS</span></div>
                    <dl>
                      <div><dt>NXDOMAIN</dt><dd>{sourceRate(source.nxdomain_pct, source.responses)}</dd></div>
                      <div><dt>SERVFAIL</dt><dd>{sourceRate(source.servfail_pct, source.responses)}</dd></div>
                      <div><dt>P95</dt><dd>{sourceLatency(source.p95_latency_us, source.responses)}</dd></div>
                      <div><dt>{t('Last event')}</dt><dd title={lastEvent ? formatDateTime(lastEvent) : undefined}>{lastEvent ? relativeTime(lastEvent) : 'N/A'}</dd></div>
                    </dl>
                  </Link>
                })}
              </div>
              <div className="resolver-traffic-share">
                <span>{t('Traffic share')}</span>
                <div className="resolver-share-bar" aria-label={t('Traffic share')}>
                  {totalResolverQPS > 0
                    ? data.sources.map((source, index) => <i className={`resolver-share-${index % 4}`} key={source.source_id} style={{ width: `${sourceQPS(source.current_qps) / totalResolverQPS * 100}%` }} />)
                    : <i className="resolver-share-empty" />}
                </div>
                <div className="resolver-share-legend">
                  {data.sources.map((source, index) => <span key={source.source_id}><i className={`resolver-share-${index % 4}`} />{source.source_id}<strong>{totalResolverQPS > 0 && Number.isFinite(source.current_qps) ? formatPercent(sourceQPS(source.current_qps) / totalResolverQPS * 100) : 'N/A'}</strong></span>)}
                </div>
              </div>
            </div>}
          </Panel>

          <div className="overview-grid">
            <Panel title={t('Top domains')} subtitle={t('Highest query volume')} action={<ViewAll to="/domains" />}>
              {data.domains.length === 0 ? <EmptyState /> : (
                <div className="table-scroll"><table className="data-table compact-table">
                  <thead><tr><th>{t('Domain')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">NXDOMAIN</th></tr></thead>
                  <tbody>{data.domains.map((row) => <tr key={row.domain}>
                    <td><Link className="cell-link" to={`/search?range=${range}&domain=${encodeURIComponent(row.domain)}`}>{row.domain}</Link></td>
                    <td className="align-right mono">{formatCompact(row.queries)}</td>
                    <td className="align-right mono muted">{formatCompact(row.nxdomain)}</td>
                  </tr>)}</tbody>
                </table></div>
              )}
            </Panel>

            <Panel title={t('Top clients')} subtitle={t('Highest query volume')} action={<ViewAll to="/clients" />}>
              {data.clients.length === 0 ? <EmptyState /> : (
                <div className="table-scroll"><table className="data-table compact-table">
                  <thead><tr><th>{t('Client')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">NXDOMAIN</th></tr></thead>
                  <tbody>{data.clients.map((row) => <tr key={row.client_ip}>
                    <td><Link className="cell-link mono" to={`/search?range=${range}&client_ip=${encodeURIComponent(row.client_ip)}`}>{row.client_ip}</Link></td>
                    <td className="align-right mono">{formatCompact(row.queries)}</td>
                    <td className="align-right mono muted">{formatPercent(row.nxdomain_pct)}</td>
                  </tr>)}</tbody>
                </table></div>
              )}
            </Panel>

            <Panel title={t('RCODE distribution')} subtitle={t('Responses in the selected range')}>
              {responseTotal === 0 ? <EmptyState /> : <div className="rcode-distribution">
                <div className="rcode-stack" aria-label={t('RCODE distribution')}>
                  {responseCodes.map((row) => <i key={row.rcode} className={`rcode-${row.rcode.toLowerCase()}`} style={{ width: `${(row.queries / responseTotal) * 100}%` }} />)}
                </div>
                <div className="rcode-legend">{responseCodes.map((row) => <div key={row.rcode}><span><i className={`rcode-${row.rcode.toLowerCase()}`} />{row.rcode}</span><strong>{formatCompact(row.queries)}</strong><small>{formatPercent((row.queries / responseTotal) * 100)}</small></div>)}</div>
              </div>}
            </Panel>

            <Panel title={t('Operational signals')} subtitle={t('Current anomaly evaluation')} action={<ViewAll to="/anomalies" />}>
              {data.anomalies.anomalies.length === 0 ? (
                <EmptyState title="No active anomalies" detail="All monitored thresholds are currently within their configured bounds." />
              ) : (
                <div className="anomaly-summary-list compact-signals">
                  {data.anomalies.anomalies.slice(0, 5).map((anomaly, index) => (
                    <div key={`${anomaly.type}-${anomaly.source_id ?? index}`}>
                      <StatusBadge value={anomaly.severity} />
                      <span><strong>{t(anomaly.title)}</strong><small>{anomaly.source_id || t('All sources')} · {anomaly.value.toFixed(1)} {t(anomaly.unit)}</small></span>
                      <time>{relativeTime(anomaly.observed_at)}</time>
                    </div>
                  ))}
                </div>
              )}
            </Panel>
          </div>
        </>
      )}
    </div>
  )
}
