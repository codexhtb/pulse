import { api } from '../api'
import { Link } from 'react-router-dom'
import { ErrorState, LoadingState, MetricCard, PageHeader, Panel, StatusBadge } from '../components/Common'
import { formatBytes, formatCompact, formatDateTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import { useI18n } from '../i18n'

function duration(seconds: number) {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

function queue(depth: number, capacity: number) {
  const percent = capacity ? (100 * depth / capacity).toFixed(1) : '0.0'
  return `${formatCompact(depth)} / ${formatCompact(capacity)} (${percent}%)`
}

export function PipelinePage() {
  const { t } = useI18n()
  const state = useAsync((signal) => api.system(signal), [], 5_000)

  return <div className="page pipeline-page">
    <PageHeader eyebrow={t('OPERATIONS')} title={t('Pipeline')} description={t('Durable ingestion, live delivery, DNS sources, and ClickHouse capacity.')} actions={state.data && <StatusBadge value={state.data.status} />} />
    {state.loading && !state.data && <LoadingState rows={8} />}
    {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
    {state.data && <>
      <section className="metric-grid metric-grid-six pipeline-summary">
        <MetricCard label={t('Ingest rate')} value={`${state.data.collector.normalized_rate_per_sec.toFixed(1)}/s`} detail={t('normalized transactions')} />
        <MetricCard label={t('Insert rate')} value={`${state.data.persistence.insert_rate_per_sec.toFixed(1)}/s`} detail={t('recent process sample')} />
        <MetricCard label={t('Pending')} value={formatCompact(state.data.collector.pending_count)} detail={t('peak {count}', { count: formatCompact(state.data.collector.pending_peak_since_start) })} />
        <MetricCard label={t('Durable dropped')} value={formatCompact(state.data.persistence.durable_dropped)} detail={t('since {time}', { time: formatDateTime(state.data.collector.process_started_at) })} tone={state.data.persistence.durable_dropped ? 'danger' : 'default'} />
        <MetricCard label={t('Write failures')} value={formatCompact(state.data.persistence.write_failures)} tone={state.data.persistence.write_failures ? 'danger' : 'default'} />
        <MetricCard label={t('Raw rows')} value={formatCompact(state.data.storage.raw_rows)} detail={state.data.storage.raw_compressed_bytes === undefined ? t('compressed size unavailable') : formatBytes(state.data.storage.raw_compressed_bytes)} />
      </section>

      <div className="pipeline-grid">
        <Panel title={t('Durable telemetry')} subtitle={t('Counters since collector start · uptime {uptime}', { uptime: duration(state.data.collector.uptime_seconds) })} action={<StatusBadge value={state.data.subsystems.durable} />}>
          <dl className="health-list">
            <div><dt>{t('Persistent queue')}</dt><dd>{queue(state.data.persistence.queue_depth, state.data.persistence.queue_capacity)}</dd></div>
            <div><dt>{t('Queued / inserted')}</dt><dd>{formatCompact(state.data.persistence.queued_total)} / {formatCompact(state.data.persistence.inserted_total)}</dd></div>
            <div><dt>{t('Batches')}</dt><dd>{formatCompact(state.data.persistence.batch_count)}</dd></div>
            <div><dt>{t('Correlation window')}</dt><dd>{state.data.collector.correlation_timeout_ms.toLocaleString()} ms</dd></div>
            <div><dt>{t('Pending persistence')}</dt><dd>{t(state.data.collector.pending_persistence.enabled ? 'enabled' : 'disabled')}</dd></div>
            <div><dt>WAL / checkpoint</dt><dd>{formatBytes(state.data.collector.pending_persistence.wal_bytes)} / {formatBytes(state.data.collector.pending_persistence.checkpoint_bytes)}</dd></div>
            <div><dt>{t('Recovered / recovery errors')}</dt><dd>{formatCompact(state.data.collector.pending_persistence.recovered_pending)} / {formatCompact(state.data.collector.pending_persistence.recovery_errors)}</dd></div>
            <div><dt>{t('Persistence lag')}</dt><dd>{state.data.collector.pending_persistence.persistence_lag_ms} ms</dd></div>
            <div><dt>{t('Last checkpoint')}</dt><dd>{state.data.collector.pending_persistence.last_checkpoint_at ? formatDateTime(state.data.collector.pending_persistence.last_checkpoint_at) : t('not yet')}</dd></div>
            <div><dt>{t('NO_RESPONSE meaning')}</dt><dd>{t('No client response observed within correlation window')}</dd></div>
          </dl>
        </Panel>

        <Panel title={t('Live telemetry')} subtitle={t('Best-effort; live loss does not imply durable history loss')} action={<StatusBadge value={state.data.subsystems.live} />}>
          <dl className="health-list">
            <div><dt>{t('Publisher queue')}</dt><dd>{queue(state.data.live.publisher.queue_depth, state.data.live.publisher.queue_capacity)}</dd></div>
            <div><dt>{t('Sent / dropped')}</dt><dd>{formatCompact(state.data.live.publisher.sent_total)} / {formatCompact(state.data.live.publisher.dropped)}</dd></div>
            <div><dt>{t('UDP errors / decode errors')}</dt><dd>{formatCompact(state.data.live.publisher.udp_errors)} / {formatCompact(state.data.live.api.decode_errors)}</dd></div>
            <div><dt>{t('SSE subscribers')}</dt><dd>{formatCompact(state.data.live.api.sse_subscribers)}</dd></div>
          </dl>
        </Panel>

        <Panel title={t('DNS sources')} subtitle={t('A TCP session alone does not prove telemetry flow')} action={<StatusBadge value={state.data.subsystems.sources} />}>
          <div className="table-scroll"><table className="data-table"><thead><tr><th>{t('Source')}</th><th>{t('Telemetry')}</th><th>{t('Connection')}</th><th>{t('Agent')}</th><th>{t('Control')}</th><th>{t('Last message')}</th><th>{t('Last persisted')}</th></tr></thead><tbody>
            {state.data.sources.map((source) => <tr key={source.source_id}><td><Link className="cell-link" to={`/sources/${encodeURIComponent(source.source_id)}`}><strong>{source.source_id}</strong></Link><small className="table-subtext mono">{t(source.expected ? 'expected' : 'unexpected')} · {source.remote_address || t('unknown remote')}</small></td><td><StatusBadge value={source.state} /></td><td>{t(source.connected ? 'established' : 'disconnected')}</td><td>{source.control ? <><StatusBadge value={source.control.health.status || 'unknown'} /><small className="table-subtext">{source.control.display_name} · {source.control.health.mode ? t(source.control.health.mode) : '—'}</small></> : '—'}</td><td>{source.control ? <StatusBadge value={source.control.control_enabled ? 'enabled' : 'disabled'} /> : '—'}</td><td className="mono muted">{source.last_message_at ? formatDateTime(source.last_message_at) : '—'}</td><td className="mono muted">{source.last_persisted_at ? formatDateTime(source.last_persisted_at) : '—'}</td></tr>)}
          </tbody></table></div>
        </Panel>

        <Panel title={t('DNS control nodes')} subtitle={t('Authenticated agents and firewall control mode')} action={<StatusBadge value={state.data.subsystems.control || 'disabled'} />}>
          {(state.data.control_nodes ?? []).length === 0 ? <div className="muted">{t('No control nodes configured')}</div> : <div className="table-scroll"><table className="data-table"><thead><tr><th>{t('DNS node')}</th><th>{t('Source')}</th><th>{t('Agent health')}</th><th>{t('Mode')}</th><th>{t('Control')}</th><th>{t('DNS service IP')}</th></tr></thead><tbody>
            {(state.data.control_nodes ?? []).map((node) => <tr key={node.id}><td><strong>{node.display_name}</strong><small className="table-subtext mono">{node.id}</small></td><td className="mono">{node.source_identity}</td><td><StatusBadge value={node.health.status || 'unknown'} /></td><td>{node.health.mode ? t(node.health.mode) : '—'}</td><td><StatusBadge value={node.control_enabled ? 'enabled' : 'disabled'} /></td><td className="mono">{node.dns_service_ip}</td></tr>)}
          </tbody></table></div>}
        </Panel>

        <Panel title={t('Storage')} subtitle={state.data.storage.filesystem_path || t('Filesystem path unavailable')} action={<StatusBadge value={state.data.storage.filesystem_status || state.data.subsystems.storage} />}>
          <dl className="health-list">
            <div><dt>{t('Filesystem used / total')}</dt><dd>{state.data.storage.disk_used_bytes === undefined ? t('unavailable') : `${formatBytes(state.data.storage.disk_used_bytes)} / ${formatBytes(state.data.storage.disk_total_bytes ?? 0)}`}</dd></div>
            <div><dt>{t('Utilization')}</dt><dd>{state.data.storage.filesystem_utilization_pct === undefined ? t('unavailable') : `${state.data.storage.filesystem_utilization_pct.toFixed(1)}%`}</dd></div>
            <div><dt>{t('Warning / critical')}</dt><dd>{state.data.storage.filesystem_warning_pct ?? '—'}% / {state.data.storage.filesystem_critical_pct ?? '—'}%</dd></div>
            <div><dt>{t('Pulse compressed')}</dt><dd>{state.data.storage.database_compressed_bytes === undefined ? t('unavailable') : formatBytes(state.data.storage.database_compressed_bytes)}</dd></div>
            <div><dt>{t('Active parts / merges')}</dt><dd>{state.data.storage.active_parts === undefined ? t('unavailable') : formatCompact(state.data.storage.active_parts)} / {state.data.storage.active_merges ?? t('unavailable')}</dd></div>
            <div><dt>{t('Oldest raw event')}</dt><dd>{state.data.storage.oldest_raw_event ? formatDateTime(state.data.storage.oldest_raw_event) : '—'}</dd></div>
            <div><dt>{t('Newest raw event')}</dt><dd>{state.data.storage.newest_raw_event ? formatDateTime(state.data.storage.newest_raw_event) : '—'}</dd></div>
            <div><dt>{t('Filesystem free')}</dt><dd>{state.data.storage.disk_free_bytes === undefined ? t('unavailable') : formatBytes(state.data.storage.disk_free_bytes)}</dd></div>
          </dl>
        </Panel>
      </div>
    </>}
  </div>
}
