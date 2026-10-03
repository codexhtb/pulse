import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel, RangePicker } from '../components/Common'
import { formatCompact, formatPercent } from '../format'
import { useAsync } from '../hooks/useAsync'
import type { TimeRange } from '../types'
import { useI18n } from '../i18n'

export function DomainsPage() {
  const { t } = useI18n()
  const [range, setRange] = useState<TimeRange>('6h')
  const state = useAsync((signal) => api.topDomains(range, 100, signal), [range], 30_000)

  return (
    <div className="page">
      <PageHeader eyebrow={t('ENTITIES')} title={t('Domains')} description={t('Queried domains ranked by observed DNS volume.')} actions={<RangePicker value={range} onChange={setRange} />} />
      <Panel title={t('Top domains')} subtitle={t('{range} window · up to 100 domains', { range })}>
        {state.loading && !state.data && <LoadingState rows={8} />}
        {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
        {state.data?.length === 0 && <EmptyState />}
        {state.data && state.data.length > 0 && <div className="table-scroll"><table className="data-table">
          <thead><tr><th>{t('Domain')}</th><th className="align-right">{t('Queries')}</th><th className="align-right">NXDOMAIN</th><th className="align-right">SERVFAIL</th><th className="align-right">{t('Error rate')}</th></tr></thead>
          <tbody>{state.data.map((domain) => {
            const errorRate = domain.queries ? ((domain.nxdomain + domain.servfail) / domain.queries) * 100 : 0
            return <tr key={domain.domain}>
              <td><Link className="cell-link" to={`/domains/${encodeURIComponent(domain.domain)}`}>{domain.domain}</Link></td>
              <td className="align-right mono">{formatCompact(domain.queries)}</td>
              <td className="align-right mono muted">{formatCompact(domain.nxdomain)}</td>
              <td className="align-right mono muted">{formatCompact(domain.servfail)}</td>
              <td className="align-right mono">{formatPercent(errorRate)}</td>
            </tr>
          })}</tbody>
        </table></div>}
      </Panel>
    </div>
  )
}
