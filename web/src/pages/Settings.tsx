import { Check, X } from 'lucide-react'
import { api } from '../api'
import { ErrorState, LoadingState, PageHeader, Panel, StatusBadge } from '../components/Common'
import { formatDateTime } from '../format'
import { useAsync } from '../hooks/useAsync'
import { useI18n } from '../i18n'
import { UsersPanel } from '../components/UsersPanel'

export function SettingsPage() {
  const { language, setLanguage, t } = useI18n()
  const state = useAsync(async (signal) => {
    const [health, meta] = await Promise.all([api.health(signal), api.meta(signal)])
    return { health, meta }
  }, [], 30_000)

  return (
    <div className="page">
      <PageHeader eyebrow={t('SYSTEM')} title={t('Settings')} description={t('Runtime capabilities reported by the Pulse API.')} actions={
        <div className="quick-filters" role="group" aria-label={t('Interface language')}>
          <button className={language === 'ru' ? 'selected' : ''} onClick={() => setLanguage('ru')}>{t('Russian')}</button>
          <button className={language === 'en' ? 'selected' : ''} onClick={() => setLanguage('en')}>English</button>
        </div>
      } />
      {state.loading && !state.data && <LoadingState rows={6} />}
      {state.error && !state.data && <ErrorState message={state.error.message} onRetry={state.refresh} />}
      {state.data && <div className="settings-grid">
        <Panel title={t('API status')} subtitle={t('Live runtime state')}>
          <dl className="definition-list">
            <div><dt>{t('Status')}</dt><dd><StatusBadge value={state.data.health.status} /></dd></div>
            <div><dt>{t('Service')}</dt><dd className="mono">{state.data.health.service}</dd></div>
            <div><dt>{t('API version')}</dt><dd className="mono">{state.data.meta.api}</dd></div>
            <div><dt>{t('Server time')}</dt><dd className="mono">{formatDateTime(state.data.health.time)}</dd></div>
            <div><dt>{t('Live transport')}</dt><dd className="mono">{state.data.meta.live.transport.toUpperCase()}</dd></div>
            <div><dt>{t('Live rate')}</dt><dd className="mono">{state.data.meta.live.default_rate}/s {t('default')} · {state.data.meta.live.maximum_rate}/s {t('max')}</dd></div>
          </dl>
        </Panel>
        <Panel title={t('Data retention')} subtitle={t('Reported by the backend')}>
          <dl className="definition-list">{Object.entries(state.data.meta.retention).map(([name, retention]) => <div key={name}><dt>{t(name.replaceAll('_', ' '))}</dt><dd className="mono">{retention}</dd></div>)}</dl>
        </Panel>
        <Panel title={t('Capabilities')} subtitle={t('Available API functions')} className="span-two">
          <div className="capability-grid">{Object.entries(state.data.meta.capabilities).map(([name, enabled]) => <div key={name}>{enabled ? <Check size={15} /> : <X size={15} />}<span>{t(name.replaceAll('_', ' '))}</span></div>)}</div>
        </Panel>
        <UsersPanel />
      </div>}
    </div>
  )
}
