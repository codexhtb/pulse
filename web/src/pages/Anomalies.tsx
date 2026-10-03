import { api } from '../api'
import { EmptyState, ErrorState, LoadingState, PageHeader, Panel, StatusBadge } from '../components/Common'
import { formatDateTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import { useI18n } from '../i18n'

export function AnomaliesPage() {
  const { t } = useI18n()
  const state = useAsync((signal) => api.anomalies(signal), [], 15_000)

  return (
    <div className="page">
      <PageHeader eyebrow={t('OPERATIONS')} title={t('Anomalies')} description={t('Current threshold deviations. These are operational signals, not confirmed security incidents.')} />
      <Panel title={t('Active anomalies')} subtitle={state.data ? t('{count} active · {window} evaluation window', { count: state.data.count, window: state.data.window }) : t('Current operational threshold state')}>
        {state.loading && !state.data && <LoadingState rows={5} />}
        {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
        {state.data?.anomalies.length === 0 && <EmptyState title="No active anomalies" detail="All monitored thresholds are within their configured bounds." />}
        {state.data && state.data.anomalies.length > 0 && <div className="table-scroll"><table className="data-table">
          <thead><tr><th>{t('Severity')}</th><th>{t('Anomaly')}</th><th className="align-right">{t('Value')}</th><th className="align-right">{t('Threshold')}</th><th>{t('Source')}</th><th>{t('Observed')}</th></tr></thead>
          <tbody>{state.data.anomalies.map((anomaly, index) => <tr key={`${anomaly.type}-${anomaly.source_id ?? index}`}>
            <td><StatusBadge value={anomaly.severity} /></td>
            <td><strong>{t(anomaly.title)}</strong><small className="table-subtext">{t(anomaly.type.replaceAll('_', ' '))}</small></td>
            <td className="align-right mono">{anomaly.value.toFixed(1)} {t(anomaly.unit)}</td>
            <td className="align-right mono muted">{anomaly.threshold} {t(anomaly.unit)}</td>
            <td>{anomaly.source_id || t('All sources')}</td>
            <td className="mono muted">{formatDateTime(anomaly.observed_at)}</td>
          </tr>)}</tbody>
        </table></div>}
      </Panel>
    </div>
  )
}
