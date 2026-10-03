import { ArrowLeft, Copy, Radio, ShieldAlert, ShieldCheck } from 'lucide-react'
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

function transactionFilters(range: TimeRange, clientIP: string, selected: TransactionFilter): QueryFilters {
  const filters: QueryFilters = { range, client_ip: clientIP, source: '', domain: '', qtype: '', rcode: '', protocol: '', outcome: '' }
  if (selected === 'failures') filters.failures = true
  if (selected === 'no_response') filters.outcome = 'NO_RESPONSE'
  if (selected === 'nxdomain') filters.rcode = 'NXDOMAIN'
  if (selected === 'servfail') filters.rcode = 'SERVFAIL'
  if (selected === 'slow') filters.slow_us = 500_000
  return filters
}

export function ClientDetailPage() {
  const { t } = useI18n()
  const { clientIP = '' } = useParams()
  const decodedIP = decodeURIComponent(clientIP)
  const [range, setRange] = useState<TimeRange>('6h')
  const [transactionFilter, setTransactionFilter] = useState<TransactionFilter>('all')
  const [blockDuration, setBlockDuration] = useState<'1h' | '24h' | 'permanent'>('1h')
  const [blockReason, setBlockReason] = useState('')
  const [confirmation, setConfirmation] = useState<'block' | 'unblock' | null>(null)
  const [controlError, setControlError] = useState<string | null>(null)
  const [controlPending, setControlPending] = useState(false)
  const state = useAsync(async (signal) => {
    const [detail, traffic, domains, search] = await Promise.all([
      api.client(decodedIP, range, signal),
      api.clientTraffic(decodedIP, range, signal),
      api.clientDomains(decodedIP, range, 12, signal),
      api.search(transactionFilters(range, decodedIP, transactionFilter), { limit: 100 }, signal),
    ])
    return { detail, traffic, domains, events: search.events }
  }, [decodedIP, range, transactionFilter], 30_000)
  const blockState = useAsync((signal) => api.clientDNSBlock(decodedIP, signal), [decodedIP], 10_000)

  const applyDNSControl = async () => {
    if (!confirmation) return
    setControlPending(true)
    setControlError(null)
    try {
      if (confirmation === 'block') await api.blockClientDNS(decodedIP, blockDuration, blockReason)
      else await api.unblockClientDNS(decodedIP)
      setConfirmation(null)
      setBlockReason('')
      blockState.refresh()
    } catch (error) {
      setControlError(error instanceof Error ? error.message : String(error))
    } finally {
      setControlPending(false)
    }
  }

  return (
    <div className="page detail-page investigation-page">
      <Link className="back-link" to="/clients"><ArrowLeft size={14} />{t('All clients')}</Link>
      <PageHeader
        eyebrow={t('CLIENT INSPECTOR')}
        title={decodedIP}
        description={state.data ? t('{sources} · last seen {time}', { sources: state.data.detail.sources.map((source) => source.source_id).join(', ') || t('No source in range'), time: formatDateTime(state.data.detail.last_seen) }) : t('DNS profile, failures, latency, history, and live trace.')}
        actions={<div className="button-row">
          {state.data && <StatusBadge value={state.data.detail.state} />}
          <button className="button secondary" onClick={() => void navigator.clipboard.writeText(decodedIP)}><Copy size={15} />{t('Copy IP')}</button>
          <Link className="button secondary" to={`/live?client_ip=${encodeURIComponent(decodedIP)}`}><Radio size={15} />{t('Watch Live')}</Link>
          <RangePicker value={range} onChange={setRange} />
        </div>}
      />
      {state.loading && !state.data && <LoadingState rows={8} />}
      {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
      {state.data && <>
        <section className="metric-grid metric-grid-eight">
          <MetricCard label={t('Queries')} value={formatCompact(state.data.detail.queries)} detail={t('{count} unique domains', { count: formatCompact(state.data.detail.unique_domains) })} />
          <MetricCard label={t('Responses')} value={formatCompact(state.data.detail.responses)} />
          <MetricCard label="NO_RESPONSE" value={formatCompact(state.data.detail.no_response)} detail={formatPercent(state.data.detail.no_response_pct)} tone={state.data.detail.no_response ? 'warning' : 'default'} />
          <MetricCard label="NXDOMAIN" value={formatCompact(state.data.detail.nxdomain)} detail={formatPercent(state.data.detail.nxdomain_pct)} />
          <MetricCard label="SERVFAIL" value={formatCompact(state.data.detail.servfail)} detail={formatPercent(state.data.detail.servfail_pct)} tone={state.data.detail.servfail ? 'danger' : 'default'} />
          <MetricCard label={t('Avg latency')} value={formatLatency(state.data.detail.avg_latency_us)} detail={t('matched responses')} />
          <MetricCard label="P50 / P95" value={`${formatLatency(state.data.detail.p50_latency_us)} / ${formatLatency(state.data.detail.p95_latency_us)}`} />
          <MetricCard label={t('P99 latency')} value={formatLatency(state.data.detail.p99_latency_us)} detail={`${formatBytes(state.data.detail.response_bytes)} ${t('response data')}`} />
        </section>

        <Panel title={t('DNS access control')} subtitle={t('Applies only to DNS traffic on TCP/UDP port 53.')}>
          {blockState.loading && !blockState.data && <LoadingState rows={2} />}
          {blockState.error && !blockState.data && <ErrorState message={blockState.error.message} onRetry={blockState.refresh} />}
          {blockState.data && <div className="filter-grid dns-control-grid">
            <dl className="health-list">
              <div><dt>{t('Desired state')}</dt><dd><StatusBadge value={blockState.data.desired} /></dd></div>
              <div><dt>{t('Convergence')}</dt><dd><StatusBadge value={blockState.data.status} /></dd></div>
              {blockState.data.reason && <div><dt>{t('Reason')}</dt><dd>{blockState.data.reason}</dd></div>}
              {blockState.data.blocked && <div><dt>{t('Expires')}</dt><dd>{blockState.data.expires_at ? formatDateTime(blockState.data.expires_at) : t('Permanent')}</dd></div>}
            </dl>
            <div className="table-scroll"><table className="data-table compact-table dns-node-table">
              <thead><tr><th>{t('DNS node')}</th><th>{t('Agent health')}</th><th>{t('Control')}</th><th>{t('Apply status')}</th><th>{t('Result')}</th></tr></thead>
              <tbody>{blockState.data.nodes.map((node) => <tr key={node.id}>
                <td><strong>{node.display_name}</strong><small className="table-subtext mono">{node.source_identity} · {node.dns_service_ip}</small></td>
                <td><StatusBadge value={node.health.status || 'unknown'} /><small className="table-subtext">{node.health.mode ? t(node.health.mode) : '—'}</small></td>
                <td><StatusBadge value={node.control_enabled ? 'enabled' : 'disabled'} /></td>
                <td><StatusBadge value={node.status} /><small className="table-subtext">{node.attempts ? t('{count} attempts', { count: node.attempts }) : '—'}</small></td>
                <td>{node.last_error || (node.agent_result ? t(node.agent_result) : '—')}{node.next_retry_at && <small className="table-subtext">{t('Retry at {time}', { time: formatDateTime(node.next_retry_at) })}</small>}</td>
              </tr>)}</tbody>
            </table></div>
            {!blockState.data.blocked && <>
              <label><span>{t('Block duration')}</span><select aria-label={t('Block duration')} value={blockDuration} onChange={(event) => setBlockDuration(event.target.value as '1h' | '24h' | 'permanent')}><option value="1h">1h</option><option value="24h">24h</option><option value="permanent">{t('Permanent')}</option></select></label>
              <label><span>{t('Reason')}</span><input aria-label={t('Reason')} value={blockReason} maxLength={256} onChange={(event) => setBlockReason(event.target.value)} placeholder={t('Why is this client being blocked?')} /></label>
              <button className="button secondary" onClick={() => setConfirmation('block')}><ShieldAlert size={15} />{t('Block DNS')}</button>
            </>}
            {blockState.data.blocked && <button className="button secondary" onClick={() => setConfirmation('unblock')}><ShieldCheck size={15} />{t('Unblock DNS')}</button>}
          </div>}
          {confirmation && <div className="pause-notice">
            <span>{confirmation === 'block'
              ? t('Confirm blocking DNS for {ip} for {duration}. This is a real firewall action.', { ip: decodedIP, duration: blockDuration === 'permanent' ? t('Permanent') : blockDuration })
              : t('Confirm unblocking DNS for {ip}.', { ip: decodedIP })}</span>
            <div className="button-row">
              <button className="button primary" disabled={controlPending} onClick={() => void applyDNSControl()}>{controlPending ? t('Applying…') : t('Confirm')}</button>
              <button className="button secondary" disabled={controlPending} onClick={() => setConfirmation(null)}>{t('Cancel')}</button>
            </div>
          </div>}
          {controlError && <div className="inline-warning">{controlError}</div>}
        </Panel>

        <Panel title={t('Client timeline')} subtitle={t('{range} · volume and outcome/latency context', { range })}><TrafficChart data={state.data.traffic} compact investigation /></Panel>

        <div className="detail-grid investigation-grid">
          <Panel title={t('Domain behavior')} subtitle={t('Top queried domains from hourly aggregates')}>
            {state.data.domains.length === 0 ? <EmptyState title="No domain activity" /> : <div className="table-scroll"><table className="data-table compact-table"><thead><tr><th>{t('Domain')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">{t('No response')}</th><th className="align-right">SERVFAIL</th></tr></thead><tbody>
              {state.data.domains.map((domain) => <tr key={domain.domain}><td><Link className="cell-link" to={`/domains/${encodeURIComponent(domain.domain)}`}>{domain.domain}</Link></td><td className="align-right mono">{formatCompact(domain.queries)}</td><td className="align-right mono">{formatCompact(domain.no_response)}</td><td className="align-right mono">{formatCompact(domain.servfail)}</td></tr>)}
            </tbody></table></div>}
          </Panel>
          <Panel title={t('Source context')} subtitle={t('Sources that observed this client in the selected range')}>
            <dl className="health-list">
              {state.data.detail.sources.map((source) => <div key={source.source_id}><dt>{source.source_id}</dt><dd>{relativeTime(source.last_seen)}</dd></div>)}
              <div><dt>{t('Unmatched responses')}</dt><dd>{formatCompact(state.data.detail.unmatched_responses)}</dd></div>
              <div><dt>{t('Exact last seen')}</dt><dd>{formatDateTime(state.data.detail.last_seen)}</dd></div>
            </dl>
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
