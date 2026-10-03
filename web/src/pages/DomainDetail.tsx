import { ArrowLeft, Copy, Radio } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, MetricCard, PageHeader, Panel, RangePicker, StatusBadge } from '../components/Common'
import { EventsTable } from '../components/EventsTable'
import { TrafficChart } from '../components/TrafficChart'
import { formatBytes, formatCompact, formatDateTime, formatLatency, formatPercent, relativeTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { QueryFilters, TimeRange } from '../types'
import { useI18n } from '../i18n'

type TransactionFilter = 'all' | 'failures' | 'no_response' | 'nxdomain' | 'servfail' | 'slow'
const filterLabels: Array<[TransactionFilter, string]> = [
  ['all', 'All'], ['failures', 'Failures'], ['no_response', 'NO_RESPONSE'], ['nxdomain', 'NXDOMAIN'], ['servfail', 'SERVFAIL'], ['slow', 'Slow'],
]

function transactionFilters(range: TimeRange, domain: string, selected: TransactionFilter): QueryFilters {
  const filters: QueryFilters = { range, domain, source: '', client_ip: '', qtype: '', rcode: '', protocol: '', outcome: '' }
  if (selected === 'failures') filters.failures = true
  if (selected === 'no_response') filters.outcome = 'NO_RESPONSE'
  if (selected === 'nxdomain') filters.rcode = 'NXDOMAIN'
  if (selected === 'servfail') filters.rcode = 'SERVFAIL'
  if (selected === 'slow') filters.slow_us = 500_000
  return filters
}

export function DomainDetailPage() {
  const { t } = useI18n()
  const { domain = '' } = useParams()
  const decodedDomain = decodeURIComponent(domain)
  const [range, setRange] = useState<TimeRange>('6h')
  const [transactionFilter, setTransactionFilter] = useState<TransactionFilter>('all')
  const state = useAsync(async (signal) => {
    const [detail, traffic, clients, search] = await Promise.all([
      api.domain(decodedDomain, range, signal),
      api.domainTraffic(decodedDomain, range, signal),
      api.domainClients(decodedDomain, range, 12, signal),
      api.search(transactionFilters(range, decodedDomain, transactionFilter), { limit: 100 }, signal),
    ])
    return { detail, traffic, clients, events: search.events }
  }, [decodedDomain, range, transactionFilter], 30_000)

  return (
    <div className="page detail-page investigation-page">
      <Link className="back-link" to="/domains"><ArrowLeft size={14} />{t('All domains')}</Link>
      <PageHeader
        eyebrow={t('DOMAIN INSPECTOR')}
        title={decodedDomain}
        description={state.data ? t('{sources} · last seen {time}', { sources: state.data.detail.sources.map((source) => source.source_id).join(', ') || t('No source in range'), time: formatDateTime(state.data.detail.last_seen) }) : t('Clients, outcomes, latency, history, and live trace.')}
        actions={<div className="button-row">
          {state.data && <StatusBadge value={state.data.detail.state} />}
          <button className="button secondary" onClick={() => void navigator.clipboard.writeText(decodedDomain)}><Copy size={15} />{t('Copy domain')}</button>
          <Link className="button secondary" to={`/live?domain=${encodeURIComponent(decodedDomain)}`}><Radio size={15} />{t('Watch Live')}</Link>
          <RangePicker value={range} onChange={setRange} />
        </div>}
      />
      {state.loading && !state.data && <LoadingState rows={8} />}
      {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
      {state.data && <>
        <section className="metric-grid metric-grid-eight">
          <MetricCard label={t('Queries')} value={formatCompact(state.data.detail.queries)} detail={t('{count} unique clients', { count: formatCompact(state.data.detail.unique_clients) })} />
          <MetricCard label={t('Responses')} value={formatCompact(state.data.detail.responses)} />
          <MetricCard label="NO_RESPONSE" value={formatCompact(state.data.detail.no_response)} detail={formatPercent(state.data.detail.no_response_pct)} tone={state.data.detail.no_response ? 'warning' : 'default'} />
          <MetricCard label="NXDOMAIN" value={formatCompact(state.data.detail.nxdomain)} detail={formatPercent(state.data.detail.nxdomain_pct)} />
          <MetricCard label="SERVFAIL" value={formatCompact(state.data.detail.servfail)} detail={formatPercent(state.data.detail.servfail_pct)} tone={state.data.detail.servfail ? 'danger' : 'default'} />
          <MetricCard label={t('Avg latency')} value={formatLatency(state.data.detail.avg_latency_us)} detail={t('matched responses')} />
          <MetricCard label="P50 / P95" value={`${formatLatency(state.data.detail.p50_latency_us)} / ${formatLatency(state.data.detail.p95_latency_us)}`} />
          <MetricCard label={t('P99 latency')} value={formatLatency(state.data.detail.p99_latency_us)} detail={`${formatBytes(state.data.detail.response_bytes)} ${t('response data')}`} />
        </section>

        <Panel title={t('Domain timeline')} subtitle={t('{range} · volume and outcome/latency context', { range })}><TrafficChart data={state.data.traffic} compact investigation /></Panel>

        <div className="detail-grid investigation-grid">
          <Panel title={t('Top clients')} subtitle={t('Clients querying this domain from hourly aggregates')}>
            {state.data.clients.length === 0 ? <EmptyState title="No client activity" /> : <div className="table-scroll"><table className="data-table compact-table"><thead><tr><th>{t('Client')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">{t('No response')}</th><th className="align-right">SERVFAIL</th></tr></thead><tbody>
              {state.data.clients.map((client) => <tr key={client.client_ip}><td><Link className="cell-link mono" to={`/clients/${encodeURIComponent(client.client_ip)}`}>{client.client_ip}</Link></td><td className="align-right mono">{formatCompact(client.queries)}</td><td className="align-right mono">{formatCompact(client.no_response)}</td><td className="align-right mono">{formatCompact(client.servfail)}</td></tr>)}
            </tbody></table></div>}
          </Panel>
          <Panel title={t('Query types and sources')} subtitle={t('Distribution in the selected window')}>
            <div className="qtype-list">{state.data.detail.qtypes.map((row) => <div key={row.qtype}><span className="query-type">{row.qtype}</span><strong className="mono">{formatCompact(row.queries)}</strong></div>)}</div>
            <dl className="health-list">{state.data.detail.sources.map((source) => <div key={source.source_id}><dt>{source.source_id}</dt><dd>{relativeTime(source.last_seen)}</dd></div>)}</dl>
          </Panel>
        </div>

        <Panel
          title={t('Recent transactions')}
          subtitle={t('Failures = NO_RESPONSE, SERVFAIL, REFUSED, or FORMERR; NXDOMAIN is separate · raw {range}', { range })}
          action={<div className="quick-filters">{filterLabels.map(([value, label]) => <button key={value} className={transactionFilter === value ? 'selected' : ''} onClick={() => setTransactionFilter(value)}>{t(label)}</button>)}</div>}
        >
          {state.data.events.length ? <EventsTable events={state.data.events} /> : <EmptyState title="No transactions for this filter" />}
        </Panel>
      </>}
    </div>
  )
}
