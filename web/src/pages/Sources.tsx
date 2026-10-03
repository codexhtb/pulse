import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel, RangePicker, StatusBadge } from '../components/Common'
import { formatCompact, formatDateTime, relativeTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { TimeRange } from '../types'
import { useI18n } from '../i18n'

export function SourcesPage() {
  const { t } = useI18n()
  const [range, setRange] = useState<TimeRange>('6h')
  const state = useAsync(async (signal) => {
    const [history, system] = await Promise.all([api.sources(range, signal), api.system(signal)])
    const counts = new Map(history.map((source) => [source.source_id, source]))
    return system.sources.map((source) => ({ ...source, queries: counts.get(source.source_id)?.queries ?? 0, historical_last_seen: counts.get(source.source_id)?.last_seen }))
  }, [range], 15_000)

  return (
    <div className="page">
      <PageHeader eyebrow={t('INGESTION')} title={t('Sources')} description={t('DNS telemetry producers observed by Pulse.')} actions={<RangePicker value={range} onChange={setRange} />} />
      <Panel title={t('Telemetry sources')} subtitle={t('{range} query window · status is derived from last event time', { range })}>
        {state.loading && !state.data && <LoadingState rows={5} />}
        {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
        {state.data?.length === 0 && <EmptyState title="No configured or observed sources" />}
        {state.data && state.data.length > 0 && <div className="table-scroll"><table className="data-table">
          <thead><tr><th>{t('Source ID')}</th><th className="align-right">{t('Queries')}</th><th>{t('Last seen')}</th><th>{t('Telemetry')}</th><th>{t('Agent / control')}</th></tr></thead>
          <tbody>{state.data.map((source) => <tr key={source.source_id}>
            <td><Link className="cell-link" to={`/sources/${encodeURIComponent(source.source_id)}`}><strong>{source.source_id}</strong></Link><small className="table-subtext">{t(source.expected ? 'expected' : 'unexpected')}</small></td>
            <td className="align-right mono">{formatCompact(source.queries)}</td>
            <td><span>{source.historical_last_seen ? relativeTime(source.historical_last_seen) : t('never')}</span><small className="table-subtext mono">{source.historical_last_seen ? formatDateTime(source.historical_last_seen) : '—'}</small></td>
            <td><StatusBadge value={source.state} /></td>
            <td>{source.control
              ? <span className="status-stack"><StatusBadge value={source.control.health.status || 'unknown'} /><StatusBadge value={source.control.control_enabled ? 'enabled' : 'disabled'} /></span>
              : <span className="muted">—</span>}</td>
          </tr>)}</tbody>
        </table></div>}
      </Panel>
    </div>
  )
}
