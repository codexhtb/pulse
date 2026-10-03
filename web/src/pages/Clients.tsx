import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel, RangePicker } from '../components/Common'
import { formatCompact, formatPercent } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { TimeRange } from '../types'
import { useI18n } from '../i18n'

export function ClientsPage() {
  const { t } = useI18n()
  const [range, setRange] = useState<TimeRange>('6h')
  const state = useAsync((signal) => api.topClients(range, 100, signal), [range], 30_000)

  return (
    <div className="page">
      <PageHeader eyebrow={t('ENTITIES')} title={t('Clients')} description={t('DNS consumers ranked by observed query volume.')} actions={<RangePicker value={range} onChange={setRange} />} />
      <Panel title={t('Top clients')} subtitle={t('{range} window · up to 100 clients', { range })}>
        {state.loading && !state.data && <LoadingState rows={8} />}
        {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
        {state.data?.length === 0 && <EmptyState />}
        {state.data && state.data.length > 0 && <div className="table-scroll"><table className="data-table">
          <thead><tr><th>{t('Client IP')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">NXDOMAIN</th><th className="align-right">SERVFAIL</th><th className="align-right">{t('NXDOMAIN rate')}</th></tr></thead>
          <tbody>{state.data.map((client) => <tr key={client.client_ip}>
            <td><Link className="cell-link mono" to={`/clients/${encodeURIComponent(client.client_ip)}`}>{client.client_ip}</Link></td>
            <td className="align-right mono">{formatCompact(client.queries)}</td>
            <td className="align-right mono muted">{formatCompact(client.nxdomain)}</td>
            <td className="align-right mono muted">{formatCompact(client.servfail)}</td>
            <td className="align-right"><span className={client.nxdomain_pct >= 20 ? 'text-warning mono' : 'mono'}>{formatPercent(client.nxdomain_pct)}</span></td>
          </tr>)}</tbody>
        </table></div>}
      </Panel>
    </div>
  )
}
